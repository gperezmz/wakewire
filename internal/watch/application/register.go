package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// RegisterInput is what `wakewire register` was given.
type RegisterInput struct {
	Harness  string
	Session  string
	Process  int // the session's process id
	Dir      string
	Repo     string
	Tracker  string
	Labels   map[domain.Meaning]string
	Marker   string
	TakeOver bool
	DryRun   bool
}

// ReadLine is one value register read, and where.
type ReadLine struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Line  int    `json:"line,omitempty"` // 0: from a flag
}

// SourceFound says what register read from the repository.
type SourceFound struct {
	File        string     `json:"file,omitempty"`
	Heading     string     `json:"heading,omitempty"`
	HeadingLine int        `json:"headingLine,omitempty"`
	Values      []ReadLine `json:"values"`
}

// Issue is a code and a sentence.
type Issue struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// RegisterResult is register's answer.
type RegisterResult struct {
	Outcome    string          `json:"outcome"` // registered, refreshed, taken-over; with --dry-run, what it would be
	DryRun     bool            `json:"dryRun,omitempty"`
	Watch      string          `json:"watch"`
	Identity   string          `json:"identity"`
	Tracker    string          `json:"tracker"`
	Forge      AccountIdentity `json:"forge"`
	TrackerAs  AccountIdentity `json:"trackerAs"`
	Holder     string          `json:"holder"`
	PID        int             `json:"pid"`
	Watched    bool            `json:"watched"`
	NotWatched []Issue         `json:"notWatched"`
	Adapter    string          `json:"adapter"` // loaded, not-loaded
	Paused     *time.Time      `json:"paused"`
	Source     SourceFound     `json:"source"`
	Replaced   string          `json:"replaced,omitempty"`
}

// Register makes the calling session the holder of the watch for this
// repository and the identity its account reads as.
func (s *Service) Register(ctx context.Context, in RegisterInput) (RegisterResult, error) {
	if in.Session == "" {
		return RegisterResult{}, domain.Refuse("no_session", "no harness session was found",
			"run it from a Claude Code session, which sets CLAUDE_CODE_SESSION_ID, or pass --session <id>")
	}
	if in.Harness == "" {
		in.Harness = "claude-code"
	}
	if in.Harness != "claude-code" {
		return RegisterResult{}, domain.Invalid(fmt.Sprintf("harness %q has no adapter", in.Harness), "use --harness claude-code")
	}
	if err := s.pruneIfDue(ctx); err != nil {
		return RegisterResult{}, err
	}
	repo, err := s.selectRepo(ctx, Selector{Repo: in.Repo, Dir: in.Dir})
	if err != nil {
		return RegisterResult{}, err
	}
	tracker, labels, found, err := s.readSource(ctx, in)
	if err != nil {
		return RegisterResult{}, err
	}
	forge, err := s.accounts.ForForge(ctx, repo)
	if err != nil {
		return RegisterResult{}, err
	}
	trackerAs := forge
	switch tracker.Kind {
	case domain.TrackerJira:
		if trackerAs, err = s.accounts.ForJira(ctx, tracker.Site); err != nil {
			return RegisterResult{}, err
		}
	case domain.TrackerGitHub:
		if forge.Service != "GitHub" {
			return RegisterResult{}, domain.Refuse("source_incomplete", "the tracker is github-issues but the repository is on "+forge.Service,
				"fix the Tracker: line, or pass --tracker")
		}
	case domain.TrackerGitLab:
		if forge.Service != "GitLab" {
			return RegisterResult{}, domain.Refuse("source_incomplete", "the tracker is gitlab-issues but the repository is on "+forge.Service,
				"fix the Tracker: line, or pass --tracker")
		}
	}
	start, running, err := s.processes.StartOf(in.Process)
	if err != nil {
		return RegisterResult{}, err
	}
	if !running {
		return RegisterResult{}, domain.Refuse("no_session", fmt.Sprintf("the session's process %d is not running", in.Process),
			"run it from the Claude Code session, which sets CLAUDE_PID")
	}
	marker := in.Marker
	if marker == "" {
		marker = s.settings.Marker
	}
	now := s.clock.Now()
	res := RegisterResult{
		DryRun: in.DryRun, Watch: repo.String(), Identity: forge.Identity, Tracker: tracker.String(),
		Forge: forge, TrackerAs: trackerAs, Holder: in.Harness + ":" + in.Session, PID: in.Process,
		Source: found, NotWatched: []Issue{},
	}
	err = s.store.Tx(ctx, func(tx Tx) error {
		w, err := tx.WatchByKey(repo.String(), forge.Identity)
		if err != nil {
			return err
		}
		res.Outcome = "registered"
		var replaced string
		if w == nil {
			w = &Watch{Repo: repo, Identity: forge.Identity, CreatedAt: now}
		} else if reg, err := tx.Registration(w.ID); err != nil {
			return err
		} else if reg != nil {
			holds, _, err := s.liveness(tx, *w, *reg)
			if err != nil {
				return err
			}
			switch {
			case reg.Session == in.Session && reg.EndedAt == nil:
				res.Outcome = "refreshed"
			case holds && !in.TakeOver:
				return domain.Refuse("held", fmt.Sprintf("%s:%s holds the watch for %s (pid %d, heard %s ago)",
					reg.Harness, reg.Session, repo, reg.Process.PID, now.Sub(reg.HeardAt).Round(time.Second)),
					"pass --take-over to take it from that session, or leave it to the session that holds it")
			default:
				res.Outcome, replaced = "taken-over", reg.Session
			}
		}
		res.Replaced = replaced
		w.ForgeAccount, w.Tracker, w.TrackerIdentity, w.TrackerAccount = forge.Account, tracker, trackerAs.Identity, trackerAs.Account
		w.Labels, w.Marker = labels, marker
		res.Paused = w.PausedSince
		if a, err := tx.Adapter(in.Session); err != nil {
			return err
		} else if a != nil && a.Process.PID == in.Process && a.Process.Start == start {
			res.Adapter = "loaded"
		} else {
			res.Adapter = "not-loaded"
		}
		if w.ID != 0 {
			probs, err := tx.OpenProblems(w.ID)
			if err != nil {
				return err
			}
			for _, p := range probs {
				res.NotWatched = append(res.NotWatched, Issue{Code: p.Code, Text: p.Text})
			}
		}
		res.Watched = len(res.NotWatched) == 0
		if in.DryRun {
			return nil
		}
		if err := tx.SaveWatch(w); err != nil {
			return err
		}
		// A session holds one watch: registering another ends its earlier one.
		if prev, err := tx.RegistrationBySession(in.Session); err != nil {
			return err
		} else if prev != nil && prev.WatchID != w.ID && prev.EndedAt == nil {
			prev.EndedAt, prev.EndReason = &now, "registered another watch"
			if err := tx.SaveRegistration(*prev); err != nil {
				return err
			}
		}
		return tx.SaveRegistration(Registration{
			WatchID: w.ID, Harness: in.Harness, Session: in.Session, Process: Process{in.Process, start},
			RegisteredAt: now, HeardAt: now, Replaced: replaced,
		})
	})
	return res, err
}

// readSource reads the tracker and labels from the source template, with
// the flags over it.
func (s *Service) readSource(ctx context.Context, in RegisterInput) (domain.Tracker, domain.Labels, SourceFound, error) {
	labels := domain.Labels{}
	var found SourceFound
	trackerText, trackerLine := in.Tracker, 0
	needFile := in.Tracker == ""
	for _, m := range domain.Meanings {
		if in.Labels[m] == "" {
			needFile = true
		}
	}
	read := domain.SourceRead{LabelLines: map[domain.Meaning]int{}}
	if needFile {
		root, err := s.repos.Root(ctx, in.Dir)
		if err != nil {
			return domain.Tracker{}, nil, found, err
		}
		ok := false
		for _, f := range s.settings.Template.Files {
			text, exists, err := s.repos.ReadFile(root, f)
			if err != nil {
				return domain.Tracker{}, nil, found, err
			}
			if !exists {
				continue
			}
			if read, ok = domain.ReadSource(s.settings.Template, f, text); ok {
				break
			}
		}
		if !ok {
			return domain.Tracker{}, nil, found, domain.Refuse("no_source",
				fmt.Sprintf("no file of %s has a %q section", strings.Join(s.settings.Template.Files, ", "), s.settings.Template.Heading),
				"add the section that set-up-unattendish writes to AGENTS.md, or pass --tracker and every --label")
		}
		found.File, found.Heading, found.HeadingLine = read.File, read.Heading, read.HeadingLine
		if trackerText == "" {
			trackerText, trackerLine = read.Tracker, read.TrackerLine
		}
	}
	if trackerText == "" {
		return domain.Tracker{}, nil, found, domain.Refuse("source_incomplete",
			fmt.Sprintf("the %q section in %s has no %s line", found.Heading, found.File, s.settings.Template.Tracker),
			fmt.Sprintf("add `%s github-issues` (or gitlab-issues, jira:<site>/<KEY>) to the section, or pass --tracker", s.settings.Template.Tracker))
	}
	tracker, err := domain.ParseTracker(trackerText)
	if err != nil {
		return domain.Tracker{}, nil, found, err
	}
	found.Values = append(found.Values, ReadLine{Name: "tracker", Value: tracker.String(), Line: trackerLine})
	var missing []string
	for _, m := range domain.Meanings {
		v, line := in.Labels[m], 0
		if v == "" {
			v, line = read.Labels[m], read.LabelLines[m]
		}
		if v == "" {
			missing = append(missing, s.settings.Template.Keys[m])
			continue
		}
		labels[m] = v
		found.Values = append(found.Values, ReadLine{Name: string(m), Value: v, Line: line})
	}
	if len(missing) > 0 {
		return domain.Tracker{}, nil, found, domain.Refuse("source_incomplete",
			fmt.Sprintf("the %q section in %s has no label row for %s", found.Heading, found.File, strings.Join(missing, ", ")),
			"add the rows to its table, or pass --label <meaning>=<label>")
	}
	return tracker, labels, found, nil
}
