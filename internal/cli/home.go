package cli

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/toon-format/toon-go"

	"github.com/gperezmz/wakewire/internal/watch/application"
)

const description = "Wakes an agent session when what it waits on changes on GitHub, GitLab or Jira"

// homeView is what wakewire alone prints: which binary this is, the watch of the
// repository it runs in and the next steps. It reads state.db and
// config.toml only, asks no service and creates no file, and a part it
// cannot read is named in its place rather than ending the answer.
func homeView(ctx context.Context, deps Deps, s streams, cmd *call) error {
	r, js := newReply(), map[string]any{}
	// set gives a fact to both answers; only the watch differs between them.
	set := func(key string, v any) {
		r.add(key, v)
		js[key] = v
	}
	set("bin", binPath(deps))
	set("description", description)

	var here *application.WatchStatus
	err := withWatch(ctx, deps, true, func(w WatchAPI) error {
		all, err := w.LocalStatus(ctx, nil)
		if err != nil {
			return err
		}
		sel := application.Selector{Dir: deps.dir(), Session: deps.Getenv("CLAUDE_CODE_SESSION_ID")}
		mine, err := w.LocalStatus(ctx, &sel)
		switch {
		case err == nil && len(mine) == 1:
			here = &mine[0]
			r.add("watch", watchSummary(mine[0], time.Now()))
			js["watch"] = mine[0]
		case err != nil:
			set("watch", "none: "+err.Error())
		}
		set("watches", len(all))
		return nil
	})
	stateErr := err
	if err != nil {
		set("state", "unreadable: "+err.Error())
	}

	accounts := -1
	if a, err := deps.Accounts(); err != nil {
		set("accounts", "unreadable: "+err.Error())
	} else if views, err := a.List(); err != nil {
		set("accounts", "unreadable: "+err.Error())
	} else {
		accounts = len(views)
		set("accounts", accounts)
	}

	switch {
	case stateErr != nil:
		r.hint("Run `wakewire status` for what is wrong with the state and its fix")
	case accounts == 0:
		r.hint("Run `wakewire account add <name> --host <host> --owner <owner> --token env:<VAR>` to add the account a watch reads as")
	case here == nil:
		r.hint("Run `wakewire register` in a repository to make this session the holder of its watch")
	case len(here.Problems) > 0:
		r.hint("Run `wakewire status` for each problem and its fix")
	default:
		r.hint("Run `wakewire ledger` to see what changed and what was done with it")
	}
	if stateErr == nil {
		r.hint("Run `wakewire status` for every watch")
	}
	r.hint("Run `wakewire doctor` when anything looks wrong")
	return answer(s, cmd, js, r)
}

// watchSummary is a watch in the fewest facts that say whether it needs anything.
func watchSummary(st application.WatchStatus, now time.Time) any {
	return withColumns(watchRow(st), []string{"polled", "held", "paused"}, watchColumns(st, now))
}

// watchRow is a watch's row in a list of watches.
func watchRow(st application.WatchStatus) toon.Object {
	return obj("watch", st.Watch, "identity", st.Identity, "holder", holderWord(st.Holder),
		"pending", st.Pending, "problems", len(st.Problems))
}

// watchColumns are a watch's columns beyond its row.
func watchColumns(st application.WatchStatus, now time.Time) map[string]any {
	return map[string]any{"tracker": st.Tracker, "polled": polledWord(st, now), "held": st.Held, "paused": pausedWord(st, now)}
}

func polledWord(st application.WatchStatus, now time.Time) string {
	if st.PolledAt == nil {
		return "never"
	}
	return ago(now, *st.PolledAt)
}

func holderWord(h *application.HolderStatus) string {
	switch {
	case h == nil:
		return "nobody"
	case h.State == "dormant":
		return "dormant"
	}
	return "held"
}

func pausedWord(st application.WatchStatus, now time.Time) string {
	switch {
	case st.PausedSince == nil:
		return "no"
	case st.PausedUntil != nil:
		return "until " + ahead(now, *st.PausedUntil)
	}
	return "since " + ago(now, *st.PausedSince)
}

// binPath is the running executable, with the home folder as ~.
func binPath(deps Deps) string {
	if deps.Executable == nil {
		return "wakewire"
	}
	exe, err := deps.Executable()
	if err != nil {
		return "wakewire"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if deps.UserHome != nil {
		if h, err := deps.UserHome(); err == nil && h != "" {
			if rest, ok := strings.CutPrefix(exe, h); ok && (rest == "" || strings.HasPrefix(rest, string(filepath.Separator))) {
				return "~" + rest
			}
		}
	}
	return exe
}
