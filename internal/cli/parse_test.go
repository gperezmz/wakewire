package cli

import (
	"strings"
	"testing"
)

// --help is text on stdout and exits 0, under --json too, wherever it stands
// on the line and whatever else is wrong with the line.
func TestHelpIsTextOnStdoutWhereverItStands(t *testing.T) {
	for _, args := range [][]string{
		{"changes", "--help"}, {"changes", "-h"}, {"changes", "-help"}, {"changes", "193", "-h"}, {"-h", "changes"},
		{"changes", "--json", "--help"}, {"--json", "-h"}, {"frob", "--help"}, {"pause", "--nonsense", "-h"},
		{"account", "add", "--help"}, {"adapter", "ack", "-h"},
	} {
		w := newFakeWorld()
		code, out, errOut := w.run(t, args...)
		if code != exitOK || errOut != "" || !strings.HasPrefix(out, "usage: wakewire") || strings.HasPrefix(out, "{") || w.opens != 0 {
			t.Errorf("%v: exit %d stderr %q, %d opens\n%s", args, code, errOut, w.opens, out)
		}
	}
	// The help is the command's own: the command the line reached.
	for args, want := range map[string]string{"changes -h": "usage: wakewire changes <batch> [<n>] [flags]\n",
		"-h changes": "usage: wakewire changes <batch> [<n>] [flags]\n", "frob -h": "usage: wakewire [<command>]\n",
		"account add --help": "usage: wakewire account add <name> [flags]\n", "adapter": "usage: wakewire adapter <command>\n"} {
		if _, out, _ := newFakeWorld().run(t, strings.Fields(args)...); !strings.HasPrefix(out, want) {
			t.Errorf("%s: help does not open with %q:\n%s", args, want, out)
		}
	}
}

// A flag's help line says what it takes, as its own placeholder or else its
// kind, whether it is required and its default; a hidden flag is accepted and
// not listed.
func TestHelpMarksRequiredFlagsAndDefaultsAndHidesTheHidden(t *testing.T) {
	for _, c := range []struct {
		args []string
		want []string
		not  []string
	}{
		{[]string{"adapter", "next", "-h"}, []string{"--session <id>  the harness session (required)\n"}, []string{"--pid"}},
		{[]string{"ledger", "-h"}, []string{"--since <duration>", "--limit <n>", "show the most recent this many; 0 for all (default 100)\n"}, []string{"<string>", "<int>"}},
		{[]string{"account", "add", "-h"}, []string{"(default stored)\n", "--owner <owner>", "--token <source>", "repeatable\n"}, []string{"<string>"}},
		{[]string{"pause", "-h"}, []string{"--for <duration>", "--identity <login>"}, []string{"<string>"}},
		{[]string{"paths", "-h"}, []string{"Examples:\n  wakewire paths --json\n", "Exit codes: 0; 2 usage.\n"}, []string{"Flags:"}},
	} {
		_, out, _ := newFakeWorld().run(t, c.args...)
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("%v lacks %q:\n%s", c.args, want, out)
			}
		}
		for _, not := range c.not {
			if strings.Contains(out, not) {
				t.Errorf("%v has %q:\n%s", c.args, not, out)
			}
		}
	}
}

// The root's help lists the commands an agent runs, and puts adapter apart,
// saying adapters call it; a group's help lists its subcommands.
func TestRootHelpPutsAdapterApart(t *testing.T) {
	_, out, _ := newFakeWorld().run(t, "--help")
	commands, adapter, ok := strings.Cut(out, "\nFor adapters, which call them; an agent never does:\n")
	if !ok || !strings.Contains(commands, "\nCommands:\n  register") || strings.Contains(commands, "  adapter") ||
		!strings.Contains(adapter, "  adapter  commands an adapter calls, never an agent\n") {
		t.Errorf("root help:\n%s", out)
	}
	_, out, _ = newFakeWorld().run(t, "adapter")
	for _, want := range []string{"\nCommands:\n  hello  ", "\n  next   ", "\n  ack    ", "\n  ended  "} {
		if !strings.Contains(out, want) {
			t.Errorf("adapter help lacks %q:\n%s", want, out)
		}
	}
}

// Whatever is spelled with one dash is the same flag as with two, a bool
// takes true or false after =, and a value is a word after the flag or after
// = only: a word that starts with - is a value just by =, or as a number.
func TestFlagSpellingsAndValues(t *testing.T) {
	w := newFakeWorld()
	w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
	for _, args := range [][]string{
		{"pause", "-dry-run", "-json"}, {"pause", "--dry-run=true", "--json"}, {"pause", "--dry-run=false", "-json=true"},
		{"pause", "--for=1h", "--json"}, {"pause", "--for", "1h", "--json"}, {"pause", "-for", "1h", "-json"},
		{"ledger", "--limit", "5", "--json"}, {"ledger", "--limit=5", "--json"}, {"ledger", "--json", "--", "o/x"},
	} {
		if code, out, _ := w.run(t, args...); code != exitOK && errorCode(t, out) == "usage" {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
	for _, c := range []struct {
		args    []string
		code    string
		message string
	}{
		{[]string{"pause", "--dry-run=maybe"}, "invalid_value", `--dry-run "maybe" is not a valid value`},
		{[]string{"pause", "--dry-run=1"}, "invalid_value", `--dry-run "1" is not a valid value`},
		{[]string{"ledger", "--limit=1.5"}, "invalid_value", `--limit "1.5" is not a valid value`},
		// A value that starts with - is given with =; --since --json is no value.
		{[]string{"ledger", "--since", "--json"}, "usage", "--since needs a value"},
		{[]string{"ledger", "--since", "-2h"}, "usage", "--since needs a value"},
		{[]string{"ledger", "--since"}, "usage", "--since needs a value"},
		{[]string{"ledger", "--since", "--"}, "usage", "--since needs a value"},
		{[]string{"ledger", "--since=-2h"}, "invalid_value", `--since "-2h" is not a duration`},
		// A number is no flag: it is a value, or an argument.
		{[]string{"ledger", "--limit", "-1"}, "invalid_value", "--limit must be 0 or more"},
		{[]string{"changes", "193", "-1"}, "invalid_value", `"-1" is not a change number`},
		{[]string{"changes", "-1"}, "invalid_value", `"-1" is not a batch number`},
		// A word that is no flag and no number is an argument only after --.
		{[]string{"ledger", "-x"}, "usage", "unknown flag -x for `wakewire ledger`"},
		{[]string{"ledger", "-1x"}, "usage", "unknown flag -1x for `wakewire ledger`"},
		{[]string{"changes", "--", "-x"}, "invalid_value", `"-x" is not a batch number`},
	} {
		code, out, errOut := newFakeWorld().run(t, append([]string{"--json"}, c.args...)...)
		e, _ := object(t, out)["error"].(map[string]any)
		if code != exitUsage || errOut != "" || e["code"] != c.code || e["message"] != c.message {
			t.Errorf("%v: exit %d stderr %q\n%s\nwant %s: %s", c.args, code, errOut, out, c.code, c.message)
		}
	}
}

// A refusal of the line comes out in the order its parts were typed: the
// first flag refused, then an unknown command, then what is missing.
func TestTheFirstRefusalOfALineIsTheOneReported(t *testing.T) {
	for _, c := range []struct {
		args    []string
		message string
	}{
		{[]string{"help", "--nonsense"}, "unknown flag --nonsense for `wakewire`"},
		{[]string{"frob", "status"}, `unknown command "frob" for ` + "`wakewire`"},
		{[]string{"ledger", "--nonsense", "--limit", "x"}, "unknown flag --nonsense for `wakewire ledger`"},
		{[]string{"ledger", "--limit", "x", "--nonsense"}, `--limit "x" is not a valid value`},
		{[]string{"adapter", "next", "--session", "a", "--session", "b", "x"}, "--session is given more than once"},
		{[]string{"adapter", "next", "x"}, "missing required flag --session"},
		{[]string{"adapter", "hello", "--protocol", "2", "x"}, "missing required flag --session"},
		{[]string{"--since", "2h", "ledger"}, "unknown flag --since for `wakewire`"},
	} {
		code, out, _ := newFakeWorld().run(t, append(c.args, "--json")...)
		e, _ := object(t, out)["error"].(map[string]any)
		if code != exitUsage || e["message"] != c.message {
			t.Errorf("%v: exit %d\n%s\nwant %q", c.args, code, out, c.message)
		}
	}
}

// A hint for another command names its watch and the identity that picks it,
// quoted for a shell, and leaves out what is empty.
func TestForWatchNamesTheWatchAndTheIdentityQuotedAndLeavesOutWhatIsEmpty(t *testing.T) {
	for _, c := range []struct{ command, watch, identity, want string }{
		{"status", "github.com/o/x", "", "wakewire status github.com/o/x"},
		{"status", "github.com/o/x", "jdoe", "wakewire status github.com/o/x --identity jdoe"},
		{"resume", "github.com/o/x", "j doe", "wakewire resume github.com/o/x --identity 'j doe'"},
		{"resume", "github.com/o/x", "o'neil", `wakewire resume github.com/o/x --identity 'o'\''neil'`},
		{"status", "", "jdoe", "wakewire status --identity jdoe"},
		{"status", "", "", "wakewire status"},
	} {
		if got := forWatch(c.command, c.watch, c.identity); got != c.want {
			t.Errorf("forWatch(%q, %q, %q) = %q, want %q", c.command, c.watch, c.identity, got, c.want)
		}
	}
}
