package cli

import (
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/watch/application"
)

// rerun gives back the command line as typed, less the flags named and their
// values, quoted as a shell needs it.
func TestRerunKeepsTheCommandAsTypedLessTheDroppedFlags(t *testing.T) {
	root := rootCommand(newFakeWorld().deps(), streams{})
	for _, tc := range []struct {
		argv []string
		drop []string
		want string
	}{
		{[]string{"ledger", "o/x", "--since", "2h", "--limit", "5"}, []string{"limit"}, "wakewire ledger o/x --since 2h"},
		{[]string{"ledger", "--limit=5", "o/x"}, []string{"limit"}, "wakewire ledger o/x"},
		{[]string{"ledger", "--limit", "5", "--since", "2h"}, []string{"since", "limit"}, "wakewire ledger"},
		{[]string{"ledger", "-limit", "5", "x"}, []string{"limit"}, "wakewire ledger x"},
		// A bool takes no value: the word after it is the next argument.
		{[]string{"pause", "--dry-run", "o/x"}, []string{"dry-run"}, "wakewire pause o/x"},
		{[]string{"pause", "--dry-run=true", "o/x"}, []string{"dry-run"}, "wakewire pause o/x"},
		// Values a shell would read are quoted; a repeated flag stays as often as it was typed.
		{[]string{"register", "--repo", "PR #41", "--label", "a=b", "--label", "c=d e"}, []string{"dry-run"},
			"wakewire register --repo 'PR #41' --label a=b --label 'c=d e'"},
		{[]string{"ledger", "--json", "--since", "2h"}, []string{"since"}, "wakewire ledger --json"},
		// The command's own words stay, a group's subcommand too.
		{[]string{"account", "add", "w", "--host", "h", "--dry-run"}, []string{"dry-run"}, "wakewire account add w --host h"},
		// What follows -- is arguments, however it reads, and stays.
		{[]string{"ledger", "--limit", "5", "--", "--limit"}, []string{"limit"}, "wakewire ledger -- --limit"},
		{nil, []string{"limit"}, "wakewire"},
	} {
		if got := parse(root, tc.argv).rerun(tc.drop...); got != tc.want {
			t.Errorf("rerun(%v, drop %v) = %q, want %q", tc.argv, tc.drop, got, tc.want)
		}
	}
}

// Under --dry-run a hint is exactly the typed command without --dry-run, so
// the next step is to run what was just rehearsed.
func TestDryRunHintsAreTheTypedCommandWithoutDryRun(t *testing.T) {
	w := newFakeWorld()
	w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
	w.watch.registerRes = application.RegisterResult{Outcome: "registered", DryRun: true, Watch: "github.com/o/x", Adapter: "loaded"}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"register", "--dry-run", "--repo", "github.com/o/x", "--label", "spec=s", "--label", "ready=r"},
			"Run `wakewire register --repo github.com/o/x --label spec=s --label ready=r` to register"},
		{[]string{"register", "--repo=github.com/o/x", "--dry-run", "--take-over"}, "Run `wakewire register --repo=github.com/o/x --take-over` to register"},
		{[]string{"pause", "--for", "1h", "--dry-run", "o/x"}, "Run `wakewire pause --for 1h o/x` to pause it"},
		{[]string{"pause", "--dry-run", "--identity", "me"}, "Run `wakewire pause --identity me` to pause it"},
		{[]string{"unregister", "--dry-run", "--identity", "me"}, "Run `wakewire unregister --identity me` to forget it"},
		{[]string{"resume", "o/x", "--dry-run"}, "Run `wakewire resume o/x` to release them as one wake on the next tick"},
	} {
		code, out, _ := w.run(t, c.args...)
		if code != exitOK || !strings.Contains(out, c.want) {
			t.Errorf("%v: exit %d, lacks %q:\n%s", c.args, code, c.want, out)
		}
		if strings.Contains(out, "--dry-run") {
			t.Errorf("%v: the hint still rehearses:\n%s", c.args, out)
		}
	}
	// A rehearsed pause does not point at resume: nothing is paused yet.
	_, out, _ := w.run(t, "pause", "--dry-run")
	if strings.Contains(out, "resume") {
		t.Errorf("pause --dry-run suggests resume:\n%s", out)
	}
	// resume itself, done, has nothing more to say; paused-or-not it stays quiet.
	if _, out, _ = w.run(t, "resume"); strings.Contains(out, "help") {
		t.Errorf("resume has a hint:\n%s", out)
	}
	// A no-op rehearsed has nothing to run.
	w.watch.pause = &application.PauseResult{Watch: "github.com/o/x", Already: true}
	if _, out, _ = w.run(t, "resume", "--dry-run"); strings.Contains(out, "Run `wakewire resume`") {
		t.Errorf("a no-op resume offers to run it:\n%s", out)
	}
}

// An unknown flag is echoed as it was typed, one dash or two, and without
// the value it was given.
func TestAnUnknownFlagIsEchoedAsTyped(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"paths", "--x"}, "unknown flag --x for `wakewire paths`"},
		{[]string{"paths", "-x"}, "unknown flag -x for `wakewire paths`"},
		{[]string{"paths", "--x=1"}, "unknown flag --x for `wakewire paths`"},
		{[]string{"paths", "-x=1"}, "unknown flag -x for `wakewire paths`"},
		{[]string{"paths", "-long"}, "unknown flag -long for `wakewire paths`"},
		{[]string{"paths", "--long"}, "unknown flag --long for `wakewire paths`"},
		{[]string{"status", "-x"}, "unknown flag -x for `wakewire status`"},
	} {
		w := newFakeWorld()
		code, out, _ := w.run(t, append(c.args, "--json")...)
		if e := object(t, out)["error"].(map[string]any); code != exitUsage || e["message"] != c.want {
			t.Errorf("%v: exit %d\n%s\nwant %q", c.args, code, out, c.want)
		}
		// In TOON the same message is the error line.
		if _, out, _ = w.run(t, c.args...); !strings.Contains(out, "error: "+c.want+"\n") {
			t.Errorf("%v:\n%s", c.args, out)
		}
	}
}

// A flag that takes one value, given twice, is a command line with two
// answers; refuse it rather than pick. The global --json is such a flag too,
// wherever the two are. --label and --owner take many.
func TestAFlagOfOneValueGivenTwiceIsUsage(t *testing.T) {
	for _, c := range []struct {
		flag string
		args []string
	}{
		{"since", []string{"ledger", "--since=2h", "--since", "3h", "--json"}},
		{"since", []string{"ledger", "--since", "2h", "--since", "2h", "--json"}},
		{"limit", []string{"ledger", "--limit", "1", "--limit=2", "--json"}},
		{"for", []string{"pause", "--for", "1h", "--for", "2h", "--json"}},
		{"dry-run", []string{"pause", "--dry-run", "--dry-run", "--json"}},
		{"session", []string{"status", "--session", "a", "o/x", "--session", "b", "--json"}},
		{"json", []string{"ledger", "--json", "--json"}},
		{"json", []string{"--json", "status", "--json"}},
		{"json", []string{"status", "--json=true", "--json"}},
	} {
		w := newFakeWorld()
		code, out, errOut := w.run(t, c.args...)
		e, _ := object(t, out)["error"].(map[string]any)
		if code != exitUsage || errOut != "" || e["code"] != "usage" || e["message"] != "--"+c.flag+" is given more than once" {
			t.Errorf("%v: exit %d\n%s", c.args, code, out)
		}
	}
	// Flags that repeat by design, and a word after -- that only reads like a flag.
	w := newFakeWorld()
	w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
	for _, args := range [][]string{
		{"register", "--json", "--dry-run", "--label", "spec=a", "--label", "ready=b"},
		{"ledger", "--json", "--since", "2h", "--", "--since"},
		{"account", "add", "w", "--host", "h", "--owner", "a", "--owner", "b", "--token", "gh:me", "--json"},
	} {
		if code, out, _ := w.run(t, args...); code != exitOK {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
}

// --fields takes only the columns a list offers beyond its default ones: any
// other name is an invalid value whose hint lists them.
func TestFieldsOfAColumnNotOnOfferIsAnInvalidValue(t *testing.T) {
	for _, c := range []struct {
		args    []string
		columns string
	}{
		{[]string{"status", "--fields", "nope"}, "tracker, polled, held, paused"},
		{[]string{"ledger", "--fields", "nope"}, "by, reason, label, code, source"},
		{[]string{"ledger", "--fields", "batch"}, "by, reason, label, code, source"}, // a default column, not an extra one
		{[]string{"changes", "193", "--fields", "body"}, "at, state, label, path, line, field, head, code"},
		{[]string{"changes", "193", "--fields", "at,nope"}, "at, state, label, path, line, field, head, code"},
		{[]string{"account", "list", "--fields", "nope"}, "owners, email, user"},
	} {
		w := newFakeWorld()
		code, out, errOut := w.run(t, c.args...)
		if code != exitUsage || errOut != "" || !strings.Contains(out, "code: invalid_value\n") || !strings.Contains(out, "its extra columns are "+c.columns) {
			t.Errorf("%v: exit %d\n%s", c.args, code, out)
		}
		if code, out, _ = w.run(t, append(c.args, "--json")...); code != exitUsage || errorCode(t, out) != "invalid_value" {
			t.Errorf("%v --json: exit %d\n%s", c.args, code, out)
		}
	}
}

// --fields drops a name it is given twice, so the header and the rows agree.
func TestFieldsDropsARepeatedColumn(t *testing.T) {
	w := newFakeWorld()
	w.acc.views = oneAccount()
	_, out, _ := w.run(t, "account", "list", "--fields", "owners,owners")
	if !strings.Contains(out, "accounts[1]{name,host,service,token,owners}:\n  personal,github.com,github,stored,o\n") {
		t.Errorf("account list:\n%s", out)
	}
	w.watch.statuses = twoWatches()
	if _, out, _ = w.run(t, "status", "--fields", "paused,tracker,paused"); !strings.Contains(out, "watches[2]{watch,identity,holder,pending,problems,paused,tracker}:") {
		t.Errorf("status:\n%s", out)
	}
	w.watch.batch = batchOf(comment("PR #41", "first"))
	if _, out, _ = w.run(t, "changes", "193", "--fields", "at,at"); !strings.Contains(out, "changes[1]{n,item,kind,by,url,at}:") {
		t.Errorf("changes:\n%s", out)
	}
	w.watch.ledger = threeChanges()
	if _, out, _ = w.run(t, "ledger", "--fields", "by,by,reason,by"); !strings.Contains(out, "changes[3]{found,item,kind,status,batch,by,reason}:") {
		t.Errorf("ledger:\n%s", out)
	}
}
