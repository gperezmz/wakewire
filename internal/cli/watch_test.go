package cli

import (
	"slices"
	"testing"

	"github.com/gperezmz/wakewire/internal/watch/application"
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

// register's --json names the accounts it reads as in camelCase, like
// every other key, and always says whether it was a dry run.
func TestRegisterJSONNamesItsAccountsInCamelCaseAndAlwaysSaysDryRun(t *testing.T) {
	for _, dry := range []bool{false, true} {
		w := newFakeWorld()
		w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
		w.watch.registerRes = application.RegisterResult{Outcome: "registered", DryRun: dry,
			Forge:     application.AccountIdentity{Account: "personal", Service: "GitHub", Identity: "jdoe", Display: "J Doe"},
			TrackerAs: application.AccountIdentity{Account: "jira", Service: "Jira", Identity: "5b10", Display: "J Doe"},
		}
		code, out, _ := w.run(t, "register", "--json")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, out)
		}
		got := object(t, out)
		if v, present := got["dryRun"]; !present || v != dry {
			t.Errorf("dryRun %v (present %v), want %v:\n%s", v, present, dry, out)
		}
		forge := got["forge"].(map[string]any)
		if forge["account"] != "personal" || forge["service"] != "GitHub" || forge["identity"] != "jdoe" || forge["display"] != "J Doe" {
			t.Errorf("forge %v", forge)
		}
		if as := got["trackerAs"].(map[string]any); as["account"] != "jira" {
			t.Errorf("trackerAs %v", as)
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
	if code, _, _ := w.run(t, "adapter", "next", "--session", "s2", "--pid", "99", "--json"); code != 0 {
		t.Fatal(code)
	}
	want := []nextCall{{"s1", 4242}, {"s2", 99}}
	if !slices.Equal(w.watch.nextCalls, want) {
		t.Fatalf("%+v", w.watch.nextCalls)
	}
}
