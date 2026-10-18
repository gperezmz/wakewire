package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	accountapp "github.com/gperezmz/wakewire/v2/internal/account/application"
	"github.com/gperezmz/wakewire/v2/internal/adapters/home"
	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
	"github.com/gperezmz/wakewire/v2/internal/wiring"
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
	ledger      *application.LedgerResult // what Ledger answers; none by default
	batch       *application.BatchView    // what Changes answers; an empty batch by default
	statuses    []application.WatchStatus
	statusErr   error
	found       application.SourceFound
	foundErr    error
	adapter     *application.Adapter
	adapterErr  error
	closed      int

	// What the adapter commands answer; the plain answer by default.
	hello    *application.HelloResult
	next     *application.NextResult
	ack      *application.AckResult
	ended    *application.EndedResult
	interval *application.IntervalResult
	pause    *application.PauseResult // what Pause and Resume answer

	// How often each kind of status ran: the home view may only read state.db.
	statusCalls, localStatusCalls int
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
	if f.pause != nil {
		return *f.pause, f.err
	}
	return application.PauseResult{Watch: "github.com/o/x", Paused: true}, f.err
}

func (f *fakeWatch) Resume(_ context.Context, sel application.Selector, dry bool) (application.PauseResult, error) {
	f.selectors, f.resumedDry = append(f.selectors, sel), append(f.resumedDry, dry)
	if f.pause != nil {
		return *f.pause, f.err
	}
	return application.PauseResult{Watch: "github.com/o/x"}, f.err
}

func (f *fakeWatch) Status(context.Context, *application.Selector) ([]application.WatchStatus, error) {
	f.statusCalls++
	return f.statuses, f.statusErr
}

// LocalStatus is Status without the account probes: the same statuses, and
// counted apart so a test can say which one a command asked for.
func (f *fakeWatch) LocalStatus(context.Context, *application.Selector) ([]application.WatchStatus, error) {
	f.localStatusCalls++
	return f.statuses, f.statusErr
}

func (f *fakeWatch) Ledger(context.Context, application.Selector, time.Duration, string) (application.LedgerResult, error) {
	if f.ledger != nil {
		return *f.ledger, f.err
	}
	return application.LedgerResult{Changes: []application.LedgerEntry{}}, f.err
}

func (f *fakeWatch) Changes(context.Context, int64) (application.BatchView, error) {
	if f.batch != nil {
		return *f.batch, f.err
	}
	return application.BatchView{}, f.err
}

func (f *fakeWatch) SetInterval(_ context.Context, sel application.Selector, d time.Duration, dry bool) (application.IntervalResult, error) {
	f.selectors, f.intervals, f.intervalDry = append(f.selectors, sel), append(f.intervals, d), append(f.intervalDry, dry)
	if f.interval != nil {
		return *f.interval, f.err
	}
	return application.IntervalResult{Watch: "github.com/o/x", Interval: d.String()}, f.err
}

func (f *fakeWatch) Prune(_ context.Context, dry bool) (application.PruneCounts, error) {
	f.pruneDry = append(f.pruneDry, dry)
	return application.PruneCounts{}, f.err
}

func (f *fakeWatch) Hello(context.Context, string, int, int) (application.HelloResult, error) {
	if f.hello != nil {
		return *f.hello, f.err
	}
	return application.HelloResult{Protocol: domain.Protocol}, f.err
}

func (f *fakeWatch) Next(_ context.Context, session string, proc int) (application.NextResult, error) {
	f.nextCalls = append(f.nextCalls, nextCall{session, proc})
	if f.next != nil {
		return *f.next, f.err
	}
	return application.NextResult{State: "idle"}, f.err
}

func (f *fakeWatch) Ack(_ context.Context, batch int64) (application.AckResult, error) {
	if f.ack != nil {
		return *f.ack, f.err
	}
	return application.AckResult{Batch: batch}, f.err
}

func (f *fakeWatch) Ended(_ context.Context, session, _ string) (application.EndedResult, error) {
	if f.ended != nil {
		return *f.ended, f.err
	}
	return application.EndedResult{Session: session}, f.err
}

func (f *fakeWatch) SourceCheck(context.Context, string) (application.SourceFound, error) {
	return f.found, f.foundErr
}

func (f *fakeWatch) AdapterOf(context.Context, string) (*application.Adapter, error) {
	return f.adapter, f.adapterErr
}

func (f *fakeWatch) Close() error { f.closed++; return nil }

// fakeAccounts is the DoctorAccounts the account and doctor commands ask: it
// records what account add, rename and remove are asked and answers with
// what the test set.
type fakeAccounts struct {
	views     []accountapp.AccountView
	listErr   error
	unused    []string
	unusedErr error
	checkErr  error

	added   []accountapp.AddRequest
	addRes  accountapp.AddResult
	renamed [][3]any
	removed [][2]any
}

func (f *fakeAccounts) Add(_ context.Context, r accountapp.AddRequest) (accountapp.AddResult, error) {
	f.added = append(f.added, r)
	return f.addRes, nil
}
func (f *fakeAccounts) List() ([]accountapp.AccountView, error) { return f.views, f.listErr }
func (f *fakeAccounts) Rename(o, n string, dry bool) (accountapp.RenameResult, error) {
	f.renamed = append(f.renamed, [3]any{o, n, dry})
	return accountapp.RenameResult{Outcome: "renamed", DryRun: dry, From: o, To: n}, nil
}
func (f *fakeAccounts) Remove(n string, dry bool) (accountapp.RemoveResult, error) {
	f.removed = append(f.removed, [2]any{n, dry})
	return accountapp.RemoveResult{Outcome: "removed", DryRun: dry, Name: n, AccountRemoved: true}, nil
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

// fakeWorld is what a run of the CLI sees: the environment, the watch service
// and the accounts.
type fakeWorld struct {
	env     map[string]string
	watch   *fakeWatch
	acc     *fakeAccounts
	paths   *home.Paths // what paths answers; /h alone by default
	openErr error       // what opening the watch service answers, when it cannot
	opens   int         // how many times a command opened the watch service
	openDry []bool
	stdin   string // what the command reads on stdin
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
		Paths: func() (home.Paths, error) {
			if w.paths != nil {
				return *w.paths, nil
			}
			return home.Paths{Home: "/h"}, nil
		},
		Watch: func(_ context.Context, dryRun bool) (WatchAPI, error) {
			w.opens++
			w.openDry = append(w.openDry, dryRun)
			if w.openErr != nil {
				return nil, w.openErr
			}
			return w.watch, nil
		},
		Accounts: func() (DoctorAccounts, error) { return w.acc, nil },
	}
}

// run executes one command line and answers its exit code, stdout and stderr.
func (w *fakeWorld) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return runDeps(w.deps(), w.stdin, args...)
}

// runDeps executes one command line with deps and stdin.
func runDeps(deps Deps, stdin string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := RunWith(context.Background(), append([]string{"wakewire"}, args...), deps, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// runReal executes one command line against the real wiring, over
// $WAKEWIRE_HOME.
func runReal(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), append([]string{"wakewire"}, args...), wiring.ReadBuild("test", "c"), strings.NewReader(""), &out, &errOut)
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

// errorCode answers the code of the error object under --json.
func errorCode(t *testing.T, out string) string {
	t.Helper()
	e, _ := object(t, out)["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}
