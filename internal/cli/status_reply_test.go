package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
)

// twoWatches is a held watch with a problem and a watch nobody holds.
func twoWatches() []application.WatchStatus {
	now := time.Now()
	polled, next, canWrite := now.Add(-10*time.Minute), now.Add(20*time.Minute+30*time.Second), true
	return []application.WatchStatus{
		{Watch: "github.com/o/x", Identity: "jdoe", Tracker: "github-issues", Interval: "1m0s", PolledAt: &polled, NextPollAt: &next,
			Holder:  &application.HolderStatus{Holder: "claude-code:s1", PID: 10, HeardAt: now.Add(-5 * time.Minute), State: "held"},
			Pending: 2, Held: 1,
			Sources: []application.SourceStatus{{Name: application.SourceForge, Service: "GitHub", Account: "personal",
				Info: application.AccountInfo{TokenSource: "stored", CanWrite: &canWrite}, Rate: &application.RateInfo{Remaining: 4000, Limit: 5000}}},
			Problems: []application.ProblemView{{Code: "token_refused", Text: "GitHub refused the token, 401", Fix: "run `wakewire account add`", Since: now.Add(-time.Hour)}}},
		{Watch: "github.com/o/y", Identity: "jdoe", Tracker: "jira"},
	}
}

func TestStatusOfSeveralWatchesIsATableWithItsProblemsAndHints(t *testing.T) {
	w := newFakeWorld()
	w.watch.statuses = twoWatches()
	code, out, errOut := w.run(t, "status")
	if code != exitOK || errOut != "" {
		t.Fatalf("exit %d stderr %q\n%s", code, errOut, out)
	}
	for _, want := range []string{
		"watches[2]{watch,identity,holder,pending,problems}:",
		"github.com/o/x,jdoe,held,2,1",
		"github.com/o/y,jdoe,nobody,0,0",
		"problems[1]{watch,code,text,fix}:",
		"github.com/o/x,token_refused,\"GitHub refused the token, 401\",run `wakewire account add`",
		"`wakewire status <repo>`", "`wakewire status --fields polled,held,paused`", "`wakewire ledger <repo>`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "tracker") {
		t.Errorf("the default columns hold a --fields column:\n%s", out)
	}
}

func TestStatusFieldsAddColumnsToTheTable(t *testing.T) {
	w := newFakeWorld()
	w.watch.statuses = twoWatches()
	code, out, _ := w.run(t, "status", "--fields", "tracker,polled,held,paused")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"watches[2]{watch,identity,holder,pending,problems,tracker,polled,held,paused}:",
		"github.com/o/x,jdoe,held,2,1,github-issues,10m ago,1,no", "github.com/o/y,jdoe,nobody,0,0,jira,never,0,no"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestStatusOfOneWatchIsItsDetail(t *testing.T) {
	w := newFakeWorld()
	w.watch.statuses = twoWatches()[:1]
	code, out, _ := w.run(t, "status")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"watch: github.com/o/x\n", "identity: jdoe\n", "tracker: github-issues\n",
		"holder:\n  by: \"claude-code:s1\"\n  state: held\n  pid: 10\n  heard: 5m ago\n",
		"poll:\n  last: 10m ago\n  every: 1m0s\n  next: in 20m\n",
		"sources[1]{name,service,account,token,canWrite,budget}:\n  forge,GitHub,personal,stored,\"true\",4000/5000\n",
		"pending: 2\n", "held: 1\n", "paused: no\n",
		"problems[1]{code,since,text,fix}:\n  token_refused,1h ago,\"GitHub refused the token, 401\",run `wakewire account add`\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	// A watch with nothing wrong and nobody holding it says so in words.
	w.watch.statuses = twoWatches()[1:]
	_, out, _ = w.run(t, "status")
	for _, want := range []string{"holder: nobody\n", "poll:\n  last: never\n", "paused: no\n", "problems: none\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("a quiet watch: stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestStatusSaysWhenThereIsNoWatchOrNoneIsHeldByTheSession(t *testing.T) {
	w := newFakeWorld()
	_, out, _ := w.run(t, "status")
	if !strings.Contains(out, "watches: 0 watches on this machine") || !strings.Contains(out, "`wakewire register`") {
		t.Errorf("no watch:\n%s", out)
	}
	w.watch.statuses = twoWatches()
	code, out, _ := w.run(t, "status", "--session", "zz")
	if code != exitOK || !strings.Contains(out, "watches: 0 of 2 watches are held by session zz") ||
		!strings.Contains(out, "`wakewire status` for every watch") {
		t.Errorf("a session that holds nothing: exit %d\n%s", code, out)
	}
	// The session that holds one gets that watch's detail.
	_, out, _ = w.run(t, "status", "--session", "s1")
	if !strings.HasPrefix(out, "watch: github.com/o/x\n") {
		t.Errorf("the holder's session:\n%s", out)
	}
	// --json lists the same, an empty list for a miss.
	_, out, _ = w.run(t, "status", "--session", "zz", "--json")
	if got, ok := object(t, out)["watches"].([]any); !ok || len(got) != 0 {
		t.Errorf("--json for a miss:\n%s", out)
	}
}
