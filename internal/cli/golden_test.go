package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	accountapp "github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/adapters/home"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata from what the commands print")

// The --json bodies below are wakewire's machine contract: the Claude Code mod
// reads only version and adapter *, and any other adapter or script may read
// the rest. Each is compared byte for byte with a golden file, so a key
// renamed, dropped, added or reordered fails here instead of in a reader. A
// change that is meant is made by running `go test ./internal/cli -update`
// and reading the diff of testdata/ before it is committed.
func TestJSONBodiesArePinnedByGoldenFiles(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	later := at.Add(90 * time.Second)
	body := "I think this is **ready**."
	cut := true
	canWrite := true
	line := &application.LineState{Watch: "github.com/o/x", PolledAt: &at, PausedSince: nil, Held: 1, Pending: 2,
		Problems: []application.Issue{{Code: "token_refused", Text: "GitHub refused the token (401)"}}, LastChangeAt: &later}
	statuses := []application.WatchStatus{
		{Watch: "github.com/o/x", Identity: "jdoe", Tracker: "github-issues", Interval: "1m0s", PolledAt: &at, NextPollAt: &later,
			Holder: &application.HolderStatus{Holder: "claude-code:s1", PID: 10, HeardAt: later, State: "held"},
			Sources: []application.SourceStatus{{Name: application.SourceForge, Service: "GitHub", Account: "personal", Identity: "jdoe",
				Info: application.AccountInfo{TokenSource: "stored", CanWrite: &canWrite},
				Rate: &application.RateInfo{Remaining: 4000, Limit: 5000, ResetAt: later}, OKAt: &at}},
			Pending: 2, Held: 1, PausedSince: &at, PausedUntil: &later,
			Problems: []application.ProblemView{{Source: application.SourceForge, Code: "token_refused", Text: "GitHub refused the token (401)",
				Fix: "wakewire account add personal", Since: at}}},
		{Watch: "github.com/o/y", Identity: "jdoe", Tracker: "jira", Interval: "5m0s", Sources: []application.SourceStatus{}, Problems: []application.ProblemView{}},
	}

	batch := &application.BatchView{Protocol: domain.Protocol, Batch: 193, Watch: "github.com/o/x", Identity: "jdoe", Since: at, Until: later,
		Changes: []domain.Change{
			{Item: "PR #41", URL: "https://github.com/o/x/pull/41", Kind: "comment", By: "alice", At: at, Body: &body, Truncated: &cut},
			{Item: "PR #41", URL: "https://github.com/o/x/pull/41", Kind: "review-comment", By: "alice", At: at, Body: &body, Path: "main.go",
				Line: 12, Hunk: "@@ -1 +1 @@"},
			{Item: "#12", URL: "https://github.com/o/x/issues/12", Kind: "labels", At: at, From: []string{"spec"}, To: []string{"ready"}, Label: "ready"},
			{Item: "PR #41", URL: "https://github.com/o/x/pull/41", Kind: "push", By: "bob", At: at, Head: "abc123",
				Commits: []domain.Commit{{ID: "abc123", Author: "bob"}}},
			{Item: "PR #41", URL: "https://github.com/o/x/pull/41", Kind: "edit", At: at, Field: "title", Diff: "-old\n+new"},
			{Item: "github.com/o/x", URL: "https://github.com/o/x", Kind: "problem", At: at, Code: "token_refused", Text: "GitHub refused the token (401)"},
		}}

	for _, c := range []struct {
		name  string
		args  []string
		setup func(w *fakeWorld)
		exit  int
	}{
		{"version", []string{"version"}, nil, exitOK},
		{"adapter_hello", []string{"adapter", "hello", "--session", "s1"}, func(w *fakeWorld) {
			w.watch.hello = &application.HelloResult{Protocol: domain.Protocol, Holds: true, Watch: "github.com/o/x", Status: line}
		}, exitOK},
		{"adapter_next_batch", []string{"adapter", "next", "--session", "s1"}, func(w *fakeWorld) {
			w.watch.next = &application.NextResult{State: "armed", By: "s2", Polled: true, Warning: "the mod is older than this binary",
				Batch: &application.Offer{ID: 193, Wake: "[wakewire] New activity:\n- PR #41: comment by alice\nRead batch 193 with `wakewire changes 193` before acting on it. If none of it needs you, a one-line reply is enough.", Count: 1}, Status: line}
		}, exitOK},
		{"adapter_next_idle", []string{"adapter", "next", "--session", "s1"}, func(w *fakeWorld) {
			w.watch.next = &application.NextResult{State: "idle"}
		}, exitOK},
		{"adapter_ack", []string{"adapter", "ack", "193"}, func(w *fakeWorld) {
			w.watch.ack = &application.AckResult{Batch: 193, Delivered: 3, Already: true}
		}, exitOK},
		{"adapter_ended", []string{"adapter", "ended", "--session", "s1", "--reason", "clear"}, func(w *fakeWorld) {
			w.watch.ended = &application.EndedResult{Session: "s1", Dormant: true, Watch: "github.com/o/x"}
		}, exitOK},
		{"changes", []string{"changes", "193"}, func(w *fakeWorld) { w.watch.batch = batch }, exitOK},
		{"changes_one", []string{"changes", "193", "2"}, func(w *fakeWorld) { w.watch.batch = batch }, exitOK},
		{"status", []string{"status"}, func(w *fakeWorld) { w.watch.statuses = statuses }, exitOK},
		{"ledger", []string{"ledger"}, func(w *fakeWorld) {
			w.watch.ledger = &application.LedgerResult{Watch: "github.com/o/x", Changes: []application.LedgerEntry{
				{ID: 1, Source: application.SourceForge, Item: "PR #41", Kind: "comment", By: "alice", At: at, FoundAt: later,
					Status: "delivered", Reason: "", Batch: 193, Label: "", Code: ""},
				{ID: 2, Source: application.SourceTracker, Item: "KAN-3", Kind: "labels", At: at, FoundAt: later, Status: "ignored", Reason: "own change", Label: "ready"},
			}}
		}, exitOK},
		{"paths", []string{"paths"}, func(w *fakeWorld) {
			w.paths = &home.Paths{Home: "/h/.wakewire", Config: "/h/.wakewire/config.toml", Credentials: "/h/.wakewire/credentials.toml", //nolint:gosec // file paths, not credentials
				State: "/h/.wakewire/state.db", Cache: "/h/.wakewire/cache"}
		}, exitOK},
		{"account_list", []string{"account", "list"}, func(w *fakeWorld) {
			w.acc.views = []accountapp.AccountView{
				{Name: "work", Host: "github.com", Service: "github", Owners: []string{"A", "B"}, TokenSource: "gh:me", User: "me"},
				{Name: "jira", Host: "s.atlassian.net", Service: "jira", Owners: []string{}, Email: "a@b.c", TokenSource: "stored"},
			}
		}, exitOK},
		{"doctor", []string{"doctor"}, func(w *fakeWorld) {
			w.acc.views = oneAccount()
			w.watch.statuses = statuses[1:]
			w.watch.found = application.SourceFound{File: "AGENTS.md", Heading: "Wakewire", HeadingLine: 12,
				Values: []application.ReadLine{{Name: "repo", Value: "github.com/o/x", Line: 13}}}
		}, exitOK},
		{"doctor_state_unreadable", []string{"doctor"}, func(w *fakeWorld) {
			w.acc.views = oneAccount()
			w.openErr = domain.Refuse("state_unreadable", "/h/state.db does not open: file is not a database", "move it aside; `wakewire paths` shows where it is")
		}, exitError},
		{"doctor_failing", []string{"doctor"}, func(w *fakeWorld) {
			w.acc.views = oneAccount()
			w.acc.checkErr = accountapp.NewError("token_refused", "GitHub refused the token (401)", "replace it with wakewire account add")
			w.watch.statuses = statuses[1:]
		}, exitError},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newFakeWorld()
			if c.setup != nil {
				c.setup(w)
			}
			code, out, errOut := w.run(t, append(c.args, "--json")...)
			if code != c.exit || errOut != "" {
				t.Fatalf("exit %d (want %d), stderr %q\n%s", code, c.exit, errOut, out)
			}
			_ = object(t, out) // one JSON object
			golden := filepath.Join("testdata", c.name+".golden")
			if *update {
				if err := os.MkdirAll("testdata", 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(out), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden) //nolint:gosec // a fixed path under testdata
			if err != nil {
				t.Fatalf("no golden file (run with -update to write it): %v", err)
			}
			if !bytes.Equal([]byte(out), want) {
				t.Errorf("the --json body of %v changed; the --json contract.\ngot:\n%s\nwant:\n%s", c.args, out, want)
			}
		})
	}
}
