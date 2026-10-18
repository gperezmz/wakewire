package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// A refusal is an answer, and answers go to stdout: stderr stays empty, as
// TOON and as JSON, for a refusal of the use case, of the command line and of
// a bug alike.
func TestARefusalLeavesStderrEmpty(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(w *fakeWorld)
		args  []string
		exit  int
		code  string
	}{
		{"a refusal", func(w *fakeWorld) { w.watch.err = domain.Refuse("held", "s1 holds the watch", "pass --take-over") }, []string{"register"}, exitError, "held"},
		{"an unknown command", nil, []string{"frob"}, exitUsage, "usage"},
		{"an unknown flag", nil, []string{"pause", "--nonsense"}, exitUsage, "usage"},
		{"a missing flag", nil, []string{"adapter", "next"}, exitUsage, "usage"},
		{"an invalid value", nil, []string{"changes", "x"}, exitUsage, "invalid_value"},
		{"an error nothing names", func(w *fakeWorld) { w.watch.statusErr = errors.New("database is locked") }, []string{"status"}, exitError, "internal"},
	} {
		for _, asJSON := range []bool{false, true} {
			w := newFakeWorld()
			if c.setup != nil {
				c.setup(w)
			}
			args := c.args
			if asJSON {
				args = append(append([]string{}, args...), "--json")
			}
			code, out, errOut := w.run(t, args...)
			if code != c.exit || errOut != "" || out == "" {
				t.Errorf("%s (json %v): exit %d, stdout %q, stderr %q", c.name, asJSON, code, out, errOut)
			}
			if asJSON && errorCode(t, out) != c.code {
				t.Errorf("%s: code %q, want %q\n%s", c.name, errorCode(t, out), c.code, out)
			}
			if !asJSON && !strings.Contains(out, "code: "+c.code+"\n") {
				t.Errorf("%s: no `code: %s` in\n%s", c.name, c.code, out)
			}
		}
	}
}

// A refusal from a use case reaches stdout untouched, under its own code
// and with its hint, and exits as cli.md's table says: 1 for what the state
// forbids, 2 for an invalid value.
func TestAUseCaseRefusalKeepsItsCodeAndExitsAsTheTableSays(t *testing.T) {
	for _, c := range []struct {
		args []string
		err  error
		exit int
		code string
	}{
		{[]string{"changes", "5"}, domain.Refuse("no_such_batch", "no batch 5", "`wakewire ledger` shows the batches"), exitError, "no_such_batch"},
		{[]string{"changes", "5", "1"}, domain.Refuse("no_such_batch", "no batch 5", "`wakewire ledger` shows the batches"), exitError, "no_such_batch"},
		{[]string{"adapter", "ack", "5"}, domain.Refuse("no_such_batch", "no batch 5", "`wakewire ledger` shows the batches"), exitError, "no_such_batch"},
		{[]string{"register"}, domain.Refuse("no_origin", "/repo has no origin remote", "name the repository"), exitError, "no_origin"},
		{[]string{"register"}, domain.Refuse("not_a_forge", `the origin remote "/x" is not a forge URL`, "name the repository"), exitError, "not_a_forge"},
		{[]string{"register"}, domain.Refuse("source_incomplete", "AGENTS.md, line 4: bogus is not a tracker", "correct the tracker row"), exitError, "source_incomplete"},
		{[]string{"register"}, domain.Refuse("held", "s1 holds the watch", "pass --take-over"), exitError, "held"},
		{[]string{"register"}, domain.Refuse("no_session", "no harness session was found", "run it from a session"), exitError, "no_session"},
		{[]string{"pause"}, domain.Refuse("not_registered", "no watch for github.com/o/x", "register"), exitError, "not_registered"},
		{[]string{"resume"}, domain.Refuse("not_registered", "no watch", "register"), exitError, "not_registered"},
		{[]string{"status"}, domain.Refuse("token_refused", "refused", "replace it"), exitError, "token_refused"},
		{[]string{"ledger", "o/x"}, domain.Refuse("ambiguous_watch", "2 identities watch github.com/o/x", "pass --identity <login>"), exitError, "ambiguous_watch"},
		{[]string{"prune"}, domain.Refuse("state_newer", "state.db is at schema version 9, newer than this binary's 3", "install a later release"), exitError, "state_newer"},
		{[]string{"adapter", "hello", "--session", "s1", "--protocol", "1"},
			domain.Refuse("protocol_mismatch", "needs update: the binary speaks protocol 2, the adapter 1", "install the binary and the mod from the same release"), exitError, "protocol_mismatch"},
		{[]string{"interval", "1m"}, domain.Invalid("bad", "fix"), exitUsage, "invalid_value"},
	} {
		w := newFakeWorld()
		w.watch.err, w.watch.statusErr = c.err, c.err
		code, out, errOut := w.run(t, c.args...)
		if code != c.exit || errOut != "" || !strings.Contains(out, "code: "+c.code+"\n") || !strings.Contains(out, "help[1]: ") {
			t.Errorf("%v: exit %d stderr %q\n%s", c.args, code, errOut, out)
		}
		code, out, _ = w.run(t, append(c.args, "--json")...)
		e, _ := object(t, out)["error"].(map[string]any)
		if code != c.exit || e["code"] != c.code || e["message"] == "" || e["hint"] == "" {
			t.Errorf("%v --json: exit %d\n%s", c.args, code, out)
		}
	}
}

// A value the flag parser cannot read is an invalid value, named by its flag
// and what the flag takes, with no parser text in it.
func TestABadFlagValueIsInvalidValueWithoutParserText(t *testing.T) {
	for _, args := range [][]string{{"ledger", "--limit", "abc"}, {"adapter", "next", "--session", "s", "--pid", "x"},
		{"adapter", "hello", "--session", "s", "--protocol", "one"}} {
		w := newFakeWorld()
		code, out, errOut := w.run(t, args...)
		if code != exitUsage || errOut != "" || !strings.Contains(out, "code: invalid_value\n") {
			t.Errorf("%v: exit %d stderr %q\n%s", args, code, errOut, out)
		}
		if strings.Contains(out, "strconv") || strings.Contains(out, "parsing") {
			t.Errorf("%v: parser text leaked:\n%s", args, out)
		}
		if code, out, _ = w.run(t, append(args, "--json")...); code != exitUsage || errorCode(t, out) != "invalid_value" {
			t.Errorf("%v --json: exit %d\n%s", args, code, out)
		}
	}
	w := newFakeWorld()
	_, out, _ := w.run(t, "ledger", "--limit", "abc", "--json")
	e := object(t, out)["error"].(map[string]any)
	if e["message"] != `--limit "abc" is not a valid value` || !strings.Contains(e["hint"].(string), "show the most recent this many") {
		t.Errorf("%v", e)
	}
}

// The error comes out as JSON for every spelling of the flag that turns JSON
// on, even though the parser never got to read it, and as TOON otherwise.
func TestErrorsAreJSONForEverySpellingOfTheJSONFlag(t *testing.T) {
	for _, spelling := range []string{"--json", "-json", "--json=true", "-json=true"} {
		w := newFakeWorld()
		code, out, _ := w.run(t, "frob", spelling)
		if code != exitUsage || errorCode(t, out) != "usage" {
			t.Errorf("frob %s: exit %d\n%s", spelling, code, out)
		}
		// The flag may come first, too.
		if code, out, _ = w.run(t, spelling, "frob"); code != exitUsage || errorCode(t, out) != "usage" {
			t.Errorf("%s frob: exit %d\n%s", spelling, code, out)
		}
		// And an unknown flag of a command is reported the same way.
		if code, out, _ = w.run(t, "pause", "--nonsense", spelling); code != exitUsage || errorCode(t, out) != "usage" {
			t.Errorf("pause --nonsense %s: exit %d\n%s", spelling, code, out)
		}
	}
	for _, spelling := range []string{"--json=false", "-json=false"} {
		w := newFakeWorld()
		code, out, _ := w.run(t, "frob", spelling)
		if code != exitUsage || !strings.HasPrefix(out, "error: ") {
			t.Errorf("frob %s: exit %d\n%s", spelling, code, out)
		}
	}
}

// --reader is gone, and a v1.0.3 mod still passes it. An older adapter is
// told the binary has moved on, as a protocol mismatch the mod shows on its
// status line, and nothing reaches the use case.
func TestTheRemovedReaderFlagIsAProtocolMismatch(t *testing.T) {
	const message = "needs update: the binary speaks protocol 2, this adapter an older one"
	for _, reader := range [][]string{{"--reader", "tool"}, {"--reader", "cli"}, {"--reader=tool"}} {
		w := newFakeWorld()
		args := append([]string{"adapter", "next", "--session", "s1"}, reader...)
		code, out, errOut := w.run(t, args...)
		if code != exitError || errOut != "" || len(w.watch.nextCalls) != 0 {
			t.Fatalf("%v: exit %d stderr %q, %d call(s)\n%s", reader, code, errOut, len(w.watch.nextCalls), out)
		}
		for _, want := range []string{"code: protocol_mismatch\n", `error: "` + message + `"` + "\n", "`claude plugin update wakewire@wakewire`"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: stdout lacks %q:\n%s", reader, want, out)
			}
		}
		if strings.Contains(out, "valid flags") || strings.Contains(out, "unknown flag") {
			t.Errorf("%v: the generic unknown-flag text hides the mismatch:\n%s", reader, out)
		}
	}
	// Under --json, which the mod passes, the same refusal is one object.
	w := newFakeWorld()
	code, out, _ := w.run(t, "adapter", "next", "--session", "s1", "--reader", "tool", "--json")
	e, _ := object(t, out)["error"].(map[string]any)
	if code != exitError || e["code"] != "protocol_mismatch" || e["message"] != message ||
		!strings.Contains(e["hint"].(string), "claude plugin update wakewire@wakewire") {
		t.Errorf("--json: exit %d\n%s", code, out)
	}
	// The flag is gone from the command's own flags, and without it the tick runs.
	w = newFakeWorld()
	if code, out, _ = w.run(t, "adapter", "next", "--session", "s1", "--json"); code != exitOK || len(w.watch.nextCalls) != 1 {
		t.Errorf("without --reader: exit %d\n%s", code, out)
	}
	// Only that command's --reader is a mismatch: elsewhere it is an unknown flag.
	w = newFakeWorld()
	if code, out, _ = w.run(t, "status", "--reader", "tool", "--json"); code != exitUsage || errorCode(t, out) != "usage" {
		t.Errorf("status --reader: exit %d\n%s", code, out)
	}
}

// A command that needs arguments says how many and shows its usage line, and
// so does one given too many: the same check for every command, counted from
// its ArgsUsage, where an optional word is in brackets.
func TestTooFewOrTooManyArgumentsAreUsageWithTheUsageLine(t *testing.T) {
	for _, c := range []struct {
		args    []string
		message string
		usage   string
	}{
		{[]string{"changes"}, "wakewire changes takes at least 1 argument(s), got 0", "usage: wakewire changes <batch> [<n>]"},
		{[]string{"changes", "1", "2", "3"}, "wakewire changes takes at most 2 argument(s), got 3: 1 2 3", "usage: wakewire changes <batch> [<n>]"},
		{[]string{"interval"}, "wakewire interval takes at least 1 argument(s), got 0", "usage: wakewire interval [<repo>] <duration|default>"},
		{[]string{"adapter", "ack"}, "wakewire adapter ack takes at least 1 argument(s), got 0", "usage: wakewire adapter ack <batch>"},
		{[]string{"account", "add", "--host", "h"}, "wakewire account add takes at least 1 argument(s), got 0", "usage: wakewire account add <name>"},
		{[]string{"account", "rename", "a"}, "wakewire account rename takes at least 2 argument(s), got 1", "usage: wakewire account rename <old> <new>"},
		{[]string{"account", "remove"}, "wakewire account remove takes at least 1 argument(s), got 0", "usage: wakewire account remove <name>"},
		{[]string{"account", "rename", "a", "b", "c"}, "wakewire account rename takes at most 2 argument(s), got 3: a b c", "usage: wakewire account rename <old> <new>"},
		{[]string{"account", "list", "x"}, "wakewire account list takes at most 0 argument(s), got 1: x", "usage: wakewire account list"},
		{[]string{"status", "a", "b"}, "wakewire status takes at most 1 argument(s), got 2: a b", "usage: wakewire status [<repo>]"},
		{[]string{"interval", "a", "1m", "x"}, "wakewire interval takes at most 2 argument(s), got 3: a 1m x", "usage: wakewire interval [<repo>] <duration|default>"},
		{[]string{"prune", "x"}, "wakewire prune takes at most 0 argument(s), got 1: x", "usage: wakewire prune"},
		{[]string{"doctor", "x"}, "wakewire doctor takes at most 0 argument(s), got 1: x", "usage: wakewire doctor"},
		{[]string{"changes", "193", "1", "2"}, "wakewire changes takes at most 2 argument(s), got 3: 193 1 2", "usage: wakewire changes <batch> [<n>]"},
	} {
		w := newFakeWorld()
		code, out, errOut := w.run(t, append(c.args, "--json")...)
		e, _ := object(t, out)["error"].(map[string]any)
		if code != exitUsage || errOut != "" || e["code"] != "usage" || e["message"] != c.message || !strings.HasPrefix(e["hint"].(string), c.usage) {
			t.Errorf("%v: exit %d\n%s\nwant %q and %q", c.args, code, out, c.message, c.usage)
		}
		// Nothing reached a use case.
		if w.opens != 0 {
			t.Errorf("%v: the service was opened", c.args)
		}
	}
	// The optional word stays optional.
	w := newFakeWorld()
	if code, out, _ := w.run(t, "changes", "193", "--json"); code != exitOK && errorCode(t, out) == "usage" {
		t.Errorf("changes 193 needs no n: exit %d\n%s", code, out)
	}
	if code, out, _ := w.run(t, "interval", "1m", "--json"); code != exitOK {
		t.Errorf("interval 1m needs no repo: exit %d\n%s", code, out)
	}
}

// A "--" ends the flags: what follows it is an argument even if it reads
// --json, so the refusal of that command line comes out as TOON.
func TestJSONAfterTheEndOfFlagsIsNotTheJSONFlag(t *testing.T) {
	w := newFakeWorld()
	code, out, _ := w.run(t, "changes", "193", "--", "--json")
	if code != exitUsage || !strings.HasPrefix(out, "error: ") || !strings.Contains(out, "code: invalid_value\n") {
		t.Errorf("exit %d\n%s", code, out)
	}
	// Before the "--" it is the flag, as ever.
	if code, out, _ = w.run(t, "changes", "--json", "193", "--", "x"); code != exitUsage || errorCode(t, out) != "invalid_value" {
		t.Errorf("--json before --: exit %d\n%s", code, out)
	}
}

// No batch or change is numbered 0 or below, nor anything but a whole
// number: changes and adapter ack refuse it as an invalid value, however it
// is written, before the store is opened.
func TestANumberBelowOneIsInvalidBeforeAnythingIsOpened(t *testing.T) {
	// --json first: after a negative number the parser takes every word as an argument.
	for _, args := range [][]string{
		{"changes", "--json", "0"}, {"changes", "--json", "--", "-1"}, {"changes", "--json", "-0"}, {"changes", "--json", "--", "-5", "2"},
		{"changes", "--json", "x"},
		{"changes", "--json", "193", "0"}, {"changes", "--json", "193", "-1"}, {"changes", "--json", "193", "x"}, {"changes", "--json", "193", "1.5"},
		{"adapter", "ack", "--json", "0"}, {"adapter", "ack", "--json", "--", "-5"}, {"adapter", "ack", "--json", "x"},
	} {
		w := newFakeWorld()
		code, out, _ := w.run(t, args...)
		if code != exitUsage || errorCode(t, out) != "invalid_value" || w.opens != 0 {
			t.Errorf("%v: exit %d, %d opens\n%s", args, code, w.opens, out)
		}
	}
}
