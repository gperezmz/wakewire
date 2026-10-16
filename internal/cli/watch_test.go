package cli

import (
	"slices"
	"testing"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// Every dry-run flag reaches the use case, and the service is opened as one
// that creates nothing.
func TestDryRunReachesTheUseCaseAndTheComposition(t *testing.T) {
	for _, args := range [][]string{
		{"register", "--dry-run"},
		{"unregister", "--dry-run"},
		{"pause", "--dry-run"},
		{"resume", "--dry-run"},
		{"interval", "1m", "--dry-run"},
		{"prune", "--dry-run"},
	} {
		w := newFakeWorld()
		w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
		if code, out, errOut := w.run(t, append(args, "--json")...); code != 0 {
			t.Fatalf("%v: exit %d\n%s%s", args, code, out, errOut)
		}
		if !slices.Equal(w.openDry, []bool{true}) {
			t.Errorf("%v: the service was opened with dry run %v", args, w.openDry)
		}
		f := w.watch
		got := [][]bool{f.unregDry, f.pausedDry, f.resumedDry, f.intervalDry, f.pruneDry}
		if args[0] == "register" {
			if len(f.registered) != 1 || !f.registered[0].DryRun {
				t.Errorf("register: %+v", f.registered)
			}
			continue
		}
		var forwarded bool
		for _, g := range got {
			if len(g) == 1 && g[0] {
				forwarded = true
			}
		}
		if !forwarded {
			t.Errorf("%v: the use case was not asked for a dry run: %+v", args, got)
		}
	}
}

func TestACommandWithoutDryRunOpensTheRealService(t *testing.T) {
	w := newFakeWorld()
	if code, _, _ := w.run(t, "prune"); code != 0 {
		t.Fatal(code)
	}
	if !slices.Equal(w.openDry, []bool{false}) || !slices.Equal(w.watch.pruneDry, []bool{false}) {
		t.Fatalf("%v %v", w.openDry, w.watch.pruneDry)
	}
}

func TestRegisterTakesTheSessionAndProcessFromTheEnvironment(t *testing.T) {
	w := newFakeWorld()
	w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
	w.env["CLAUDE_PID"] = "777"
	if code, _, _ := w.run(t, "register", "--take-over"); code != 0 {
		t.Fatal(code)
	}
	in := w.watch.registered[0]
	if in.Session != "s1" || in.Harness != "claude-code" || in.Process != 777 || !in.TakeOver || in.Dir != "/repo" {
		t.Fatalf("%+v", in)
	}
	w = newFakeWorld() // no CLAUDE_PID: the parent process
	w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
	w.run(t, "register")
	if got := w.watch.registered[0].Process; got != 4242 {
		t.Fatalf("pid %d", got)
	}
}

// What the use cases refuse with becomes the exit code cli.md lists.
func TestRefusalsExitAsTheTableSays(t *testing.T) {
	for _, c := range []struct {
		args []string
		err  error
		code int
		name string
	}{
		{[]string{"register"}, domain.Refuse("held", "s1 holds the watch", "pass --take-over"), exitRefused, "held"},
		{[]string{"register"}, domain.Refuse("no_session", "no harness session was found", "run it from a session"), exitNotSetUp, "no_session"},
		{[]string{"pause"}, domain.Refuse("not_registered", "no watch for github.com/o/x", "register"), exitRefused, "not_registered"},
		{[]string{"resume"}, domain.Refuse("not_registered", "no watch", "register"), exitRefused, "not_registered"},
		{[]string{"status"}, domain.Refuse("token_refused", "refused", "replace it"), exitService, "token_refused"},
		{[]string{"register"}, domain.Refuse("source_incomplete", "no tracker line", "add it"), exitNotSetUp, "source_incomplete"},
		{[]string{"interval", "1m"}, domain.Invalid("bad", "fix"), exitUsage, "invalid_value"},
	} {
		w := newFakeWorld()
		w.watch.err, w.watch.statusErr = c.err, c.err
		code, out, _ := w.run(t, append(c.args, "--json")...)
		if code != c.code || errorCode(t, out) != c.name {
			t.Errorf("%v: exit %d, code %q\n%s", c.args, code, errorCode(t, out), out)
		}
		e := object(t, out)["error"].(map[string]any)
		if e["message"] == "" || e["hint"] == "" {
			t.Errorf("%v: the error lacks its message or hint: %v", c.args, e)
		}
	}
}

// The adapter commands the mod calls: their flags reach the use case, and
// the session's process is the caller's parent unless --pid says otherwise.
func TestAdapterNextReachesTheUseCaseWithTheSessionsProcess(t *testing.T) {
	w := newFakeWorld()
	if code, out, _ := w.run(t, "adapter", "next", "--session", "s1", "--json"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if code, _, _ := w.run(t, "adapter", "next", "--session", "s2", "--pid", "99", "--reader", "tool", "--json"); code != 0 {
		t.Fatal(code)
	}
	want := []nextCall{{"s1", 4242}, {"s2", 99}}
	if !slices.Equal(w.watch.nextCalls, want) {
		t.Fatalf("%+v", w.watch.nextCalls)
	}
	code, out, _ := w.run(t, "adapter", "next", "--json")
	if code != exitUsage || errorCode(t, out) != "invalid_value" {
		t.Fatalf("a missing --session: exit %d\n%s", code, out)
	}
}
