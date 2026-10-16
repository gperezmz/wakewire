package application

import (
	"context"
	"fmt"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// Settings are the configuration's values the watch context reads.
type Settings struct {
	Interval time.Duration
	Marker   string
	Template domain.Template
}

// Service holds every watch use case.
type Service struct {
	store     Store
	sources   Sources
	accounts  Accounts
	repos     Repos
	processes Processes
	clock     Clock
	settings  Settings
}

// New builds the service from its ports.
func New(store Store, sources Sources, accounts Accounts, repos Repos, processes Processes, clock Clock, settings Settings) *Service {
	if settings.Interval == 0 {
		settings.Interval = domain.DefaultInterval
	}
	if settings.Marker == "" {
		settings.Marker = domain.DefaultMarker
	}
	return &Service{store, sources, accounts, repos, processes, clock, settings}
}

func (s *Service) interval(w Watch) time.Duration {
	if w.Interval > 0 {
		return w.Interval
	}
	return s.settings.Interval
}

// liveness works out whether a registration holds its watch.
func (s *Service) liveness(tx Tx, w Watch, r Registration) (bool, string, error) {
	start, running, err := s.processes.StartOf(r.Process.PID)
	if err != nil {
		return false, "", err
	}
	cleared := false
	if running && start == r.Process.Start {
		others, err := tx.AdaptersOf(r.Process)
		if err != nil {
			return false, "", err
		}
		for _, a := range others {
			if a.Session != r.Session && a.HelloAt.After(r.RegisteredAt) {
				cleared = true
			}
		}
	}
	holds, why := domain.Liveness(domain.HolderFacts{
		ProcessRunning: running && start == r.Process.Start,
		Ended:          r.EndedAt != nil,
		Cleared:        cleared,
		HeardAt:        r.HeardAt,
		Interval:       s.interval(w),
		Now:            s.clock.Now(),
	})
	return holds, why, nil
}

func notRegistered(repo string) *domain.Refusal {
	return domain.Refuse("not_registered", fmt.Sprintf("no watch for %s", repo),
		"`wakewire register` in the repository starts one; `wakewire status` lists the watches")
}

// pick finds the one watch a command names: by repository, and by identity
// where two identities watch one repository.
func (s *Service) pick(ctx context.Context, tx Tx, sel Selector) (*Watch, error) {
	repo, err := s.selectRepo(ctx, sel)
	if err != nil {
		return nil, err
	}
	all, err := tx.Watches()
	if err != nil {
		return nil, err
	}
	var found []Watch
	for _, w := range all {
		if w.Repo == repo && (sel.Identity == "" || w.Identity == sel.Identity) {
			found = append(found, w)
		}
	}
	switch len(found) {
	case 0:
		return nil, notRegistered(repo.String())
	case 1:
		return &found[0], nil
	}
	if sel.Session != "" {
		if r, err := tx.RegistrationBySession(sel.Session); err == nil && r != nil {
			for i := range found {
				if found[i].ID == r.WatchID {
					return &found[i], nil
				}
			}
		}
	}
	return nil, domain.Invalid(fmt.Sprintf("%d identities watch %s", len(found), repo),
		"pass --identity <login>; `wakewire status` lists them")
}

// Selector names a watch from a command line.
type Selector struct {
	Repo     string // "" for the repository in Dir
	Identity string
	Session  string // the calling session, to prefer the watch it holds
	Dir      string
}

func (s *Service) selectRepo(ctx context.Context, sel Selector) (domain.Repo, error) {
	if sel.Repo != "" {
		return domain.ParseRepo(sel.Repo)
	}
	return s.repos.Origin(ctx, sel.Dir)
}

// pruneIfDue runs the daily clean-up when the last one is a day old.
func (s *Service) pruneIfDue(ctx context.Context) error {
	var due bool
	err := s.store.Tx(ctx, func(tx Tx) error {
		at, err := tx.PrunedAt()
		due = err == nil && s.clock.Now().Sub(at) >= 24*time.Hour
		return err
	})
	if err != nil || !due {
		return err
	}
	_, err = s.Prune(ctx, false)
	return err
}

// Prune runs the clean-up now; see state.md, "Pruning".
func (s *Service) Prune(ctx context.Context, dryRun bool) (PruneCounts, error) {
	var counts PruneCounts
	now := s.clock.Now()
	err := s.store.Tx(ctx, func(tx Tx) error {
		watches := map[int64]Watch{}
		all, err := tx.Watches()
		if err != nil {
			return err
		}
		for _, w := range all {
			watches[w.ID] = w
		}
		plan := PrunePlan{Now: now, Before: now.Add(-domain.KeepFor), Dormant: func(r Registration) bool {
			holds, _, err := s.liveness(tx, watches[r.WatchID], r)
			return err == nil && !holds
		}}
		counts, err = tx.Prune(plan, dryRun)
		return err
	})
	return counts, err
}
