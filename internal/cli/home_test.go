package cli

import (
	"slices"
	"strings"
	"testing"
	"time"

	accountapp "github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

func TestHomeViewWithNoWatchSaysSoAndHintsAtRegister(t *testing.T) {
	w := newFakeWorld()
	code, out, errOut := w.run(t)
	if code != exitOK || errOut != "" {
		t.Fatalf("exit %d stderr %q\n%s", code, errOut, out)
	}
	for _, want := range []string{"bin: ", `description: "Wakes an agent session`, "watches: 0", "accounts: 0",
		"`wakewire account add ", "`wakewire status`", "`wakewire doctor`"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	// With an account but no watch here, the first hint is to register one.
	w = newFakeWorld()
	w.acc.views = oneAccount()
	_, out, _ = w.run(t)
	if !strings.Contains(out, "accounts: 1") || !strings.Contains(out, "Run `wakewire register` in a repository") {
		t.Errorf("the register hint is missing:\n%s", out)
	}
}

// Running wakewire alone must change nothing and stay offline: the service
// is opened as a dry run, so it creates no folder or state.db on a machine
// that never ran it, and it reads state.db through LocalStatus, never Status,
// which asks every account's service.
func TestHomeViewOpensTheServiceDryAndReadsLocalStatusOnly(t *testing.T) {
	for _, args := range [][]string{{}, {"--json"}} {
		w := newFakeWorld()
		w.watch.statuses = []application.WatchStatus{{Watch: "github.com/o/x", Identity: "jdoe"}}
		if code, out, _ := w.run(t, args...); code != exitOK {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
		if len(w.openDry) == 0 || slices.Contains(w.openDry, false) || w.watch.closed != w.opens {
			t.Errorf("%v: opened with dry run %v, %d opens, %d closes", args, w.openDry, w.opens, w.watch.closed)
		}
		if w.watch.statusCalls != 0 || w.watch.localStatusCalls == 0 {
			t.Errorf("%v: Status ran %d time(s), LocalStatus %d", args, w.watch.statusCalls, w.watch.localStatusCalls)
		}
	}
	// status, by contrast, is the command that probes.
	w := newFakeWorld()
	if code, out, _ := w.run(t, "status"); code != exitOK || w.watch.statusCalls == 0 || w.watch.localStatusCalls != 0 {
		t.Errorf("status: exit %d, Status %d, LocalStatus %d\n%s", code, w.watch.statusCalls, w.watch.localStatusCalls, out)
	}
}

func TestHomeViewSummarisesThisRepositorysWatch(t *testing.T) {
	// Ten minutes, not seconds: the view rounds down to the minute, so a slow
	// run cannot move it to another number.
	polled := time.Now().Add(-10 * time.Minute)
	w := newFakeWorld()
	w.acc.views = oneAccount()
	w.watch.statuses = []application.WatchStatus{{Watch: "github.com/o/x", Identity: "jdoe", PolledAt: &polled, Pending: 2}}
	code, out, _ := w.run(t)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// The watch's keys come in the order of a status row, then the extras.
	if want := "watch:\n  watch: github.com/o/x\n  identity: jdoe\n  holder: nobody\n  pending: 2\n  problems: 0\n  polled: 10m ago\n  held: 0\n  paused: no\n"; !strings.Contains(out, want) {
		t.Errorf("the watch object:\n%s\nlacks\n%s", out, want)
	}
	for _, want := range []string{"watches: 1\n", "accounts: 1\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	// Extra arguments are not a command: wakewire frob is refused, not the home view.
	if code, out, _ = w.run(t, "frob"); code != exitUsage || strings.Contains(out, "bin:") {
		t.Errorf("unknown command: exit %d\n%s", code, out)
	}
}

func TestHomeViewNamesWhatItCannotRead(t *testing.T) {
	w := newFakeWorld()
	w.acc.listErr = accountapp.NewError(accountapp.CodeConfigInvalid, "config.toml does not parse", "fix it")
	code, out, _ := w.run(t)
	if code != exitOK || !strings.Contains(out, `accounts: "unreadable: config.toml does not parse"`) {
		t.Errorf("exit %d\n%s", code, out)
	}
}

// An unreadable state is named in the home view's own words, the view still
// answers, and its next step is the command that explains the state.
func TestHomeViewNamesAnUnreadableStateAndHintsAtStatus(t *testing.T) {
	w := newFakeWorld()
	w.openErr = domain.Refuse("state_unreadable", "/h/state.db does not open: file is not a database", "move it aside")
	code, out, errOut := w.run(t)
	if code != exitOK || errOut != "" {
		t.Fatalf("exit %d stderr %q\n%s", code, errOut, out)
	}
	for _, want := range []string{"state: \"unreadable: /h/state.db does not open: file is not a database\"\n", "accounts: 0\n",
		"Run `wakewire status` for what is wrong with the state and its fix", "Run `wakewire doctor` when anything looks wrong"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "for every watch") || strings.Contains(out, "watches:") {
		t.Errorf("the view claims a watch count it could not read:\n%s", out)
	}
	_, out, _ = w.run(t, "--json")
	if got, _ := object(t, out)["state"].(string); !strings.HasPrefix(got, "unreadable: ") {
		t.Errorf("--json state %q", got)
	}
}
