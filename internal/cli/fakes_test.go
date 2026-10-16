package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	accountapp "github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/adapters/home"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
	"github.com/gperezmz/wakewire/internal/wiring"
)

// fakeWatch is a WatchAPI that records what the commands ask of it and
// answers with what the test set.
type fakeWatch struct {
	err error // answered by every call that has no own error

	registered  []application.RegisterInput
	unregDry    []bool
	pausedDry   []bool
	pausedFor   []time.Duration
	resumedDry  []bool
	intervals   []time.Duration
	intervalDry []bool
	pruneDry    []bool
	selectors   []application.Selector
	nextCalls   []nextCall

	registerRes application.RegisterResult
	statuses    []application.WatchStatus
	statusErr   error
	found       application.SourceFound
	foundErr    error
	adapter     *application.Adapter
	adapterErr  error
	closed      int
}

type nextCall struct {
	Session string
	Proc    int
}

func (f *fakeWatch) Register(_ context.Context, in application.RegisterInput) (application.RegisterResult, error) {
	f.registered = append(f.registered, in)
	return f.registerRes, f.err
}

func (f *fakeWatch) Unregister(_ context.Context, sel application.Selector, dry bool) (application.UnregisterResult, error) {
	f.selectors, f.unregDry = append(f.selectors, sel), append(f.unregDry, dry)
	return application.UnregisterResult{Watch: "github.com/o/x", Identity: "jdoe", Removed: !dry}, f.err
}

func (f *fakeWatch) Pause(_ context.Context, sel application.Selector, d time.Duration, dry bool) (application.PauseResult, error) {
	f.selectors, f.pausedFor, f.pausedDry = append(f.selectors, sel), append(f.pausedFor, d), append(f.pausedDry, dry)
	return application.PauseResult{Watch: "github.com/o/x", Paused: true}, f.err
}

func (f *fakeWatch) Resume(_ context.Context, sel application.Selector, dry bool) (application.PauseResult, error) {
	f.selectors, f.resumedDry = append(f.selectors, sel), append(f.resumedDry, dry)
	return application.PauseResult{Watch: "github.com/o/x"}, f.err
}

func (f *fakeWatch) Status(context.Context, *application.Selector) ([]application.WatchStatus, error) {
	return f.statuses, f.statusErr
}

func (f *fakeWatch) Ledger(context.Context, application.Selector, time.Duration, string) (application.LedgerResult, error) {
	return application.LedgerResult{Changes: []application.LedgerEntry{}}, f.err
}

func (f *fakeWatch) Changes(context.Context, int64) (application.BatchView, error) {
	return application.BatchView{}, f.err
}

func (f *fakeWatch) SetInterval(_ context.Context, sel application.Selector, d time.Duration, dry bool) (application.IntervalResult, error) {
	f.selectors, f.intervals, f.intervalDry = append(f.selectors, sel), append(f.intervals, d), append(f.intervalDry, dry)
	return application.IntervalResult{Watch: "github.com/o/x", Interval: d.String()}, f.err
}

func (f *fakeWatch) Prune(_ context.Context, dry bool) (application.PruneCounts, error) {
	f.pruneDry = append(f.pruneDry, dry)
	return application.PruneCounts{}, f.err
}

func (f *fakeWatch) Hello(context.Context, string, int, int) (application.HelloResult, error) {
	return application.HelloResult{Protocol: domain.Protocol}, f.err
}

func (f *fakeWatch) Next(_ context.Context, session string, proc int, _ domain.Reader) (application.NextResult, error) {
	f.nextCalls = append(f.nextCalls, nextCall{session, proc})
	return application.NextResult{State: "idle"}, f.err
}

func (f *fakeWatch) Ack(_ context.Context, batch int64) (application.AckResult, error) {
	return application.AckResult{Batch: batch}, f.err
}

func (f *fakeWatch) Ended(_ context.Context, session, _ string) (application.EndedResult, error) {
	return application.EndedResult{Session: session}, f.err
}

func (f *fakeWatch) SourceCheck(context.Context, string) (application.SourceFound, error) {
	return f.found, f.foundErr
}

func (f *fakeWatch) AdapterOf(context.Context, string) (*application.Adapter, error) {
	return f.adapter, f.adapterErr
}

func (f *fakeWatch) Close() error { f.closed++; return nil }

// fakeAccounts is the DoctorAccounts the doctor command asks.
type fakeAccounts struct {
	views     []accountapp.AccountView
	listErr   error
	unused    []string
	unusedErr error
	checkErr  error
}

func (f *fakeAccounts) Add(context.Context, accountapp.AddRequest) (accountapp.AddResult, error) {
	return accountapp.AddResult{}, nil
}
func (f *fakeAccounts) List() ([]accountapp.AccountView, error) { return f.views, f.listErr }
func (f *fakeAccounts) Rename(string, string, bool) (accountapp.RenameResult, error) {
	return accountapp.RenameResult{}, nil
}
func (f *fakeAccounts) Remove(string, bool) (accountapp.RemoveResult, error) {
	return accountapp.RemoveResult{}, nil
}
func (f *fakeAccounts) ForRepo(host, _ string) (accountapp.Credential, error) {
	return accountapp.Credential{Host: host}, nil
}
func (f *fakeAccounts) ForSite(host string) (accountapp.Credential, error) {
	return accountapp.Credential{Host: host}, nil
}
func (f *fakeAccounts) Check(context.Context, accountapp.Credential, []string) (accountapp.Probe, error) {
	return accountapp.Probe{Identity: accountapp.Identity{Login: "jdoe"}}, f.checkErr
}
func (f *fakeAccounts) UnusedStored() ([]string, error) { return f.unused, f.unusedErr }

// world is what a run of the CLI sees: the environment, the watch service
// and the accounts.
type fakeWorld struct {
	env     map[string]string
	watch   *fakeWatch
	acc     *fakeAccounts
	opens   int // how many times a command opened the watch service
	openDry []bool
}

func newFakeWorld() *fakeWorld {
	return &fakeWorld{env: map[string]string{}, watch: &fakeWatch{}, acc: &fakeAccounts{}}
}

func (w *fakeWorld) deps() Deps {
	return Deps{
		Build:  wiring.Build{Version: "test", Commit: "c", Protocol: domain.Protocol},
		Getenv: func(k string) string { return w.env[k] },
		Getwd:  func() (string, error) { return "/repo", nil },
		PPID:   func() int { return 4242 },
		Paths:  func() (home.Paths, error) { return home.Paths{Home: "/h"}, nil },
		Watch: func(_ context.Context, dryRun bool) (WatchAPI, error) {
			w.opens++
			w.openDry = append(w.openDry, dryRun)
			return w.watch, nil
		},
		Accounts: func() (DoctorAccounts, error) { return w.acc, nil },
	}
}

// run executes one command line and answers its exit code, stdout and stderr.
func (w *fakeWorld) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := RunWith(context.Background(), append([]string{"wakewire"}, args...), w.deps(), strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// object parses stdout as the one JSON object --json promises.
func object(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	return m
}
