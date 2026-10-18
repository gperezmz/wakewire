package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestAnErrorNothingNamesIsReportedAsInternal(t *testing.T) {
	w := newFakeWorld()
	w.watch.statusErr = errors.New("database is locked")
	code, out, _ := w.run(t, "status", "--json")
	if code != exitError || errorCode(t, out) != "internal" {
		t.Fatalf("exit %d\n%s", code, out)
	}
	e := object(t, out)["error"].(map[string]any)
	if e["message"] != "database is locked" || !strings.Contains(e["hint"].(string), "bug in wakewire") {
		t.Fatalf("%v", e)
	}
	// Without --json the same error goes to stdout, as TOON, and stderr stays empty.
	code, out, errOut := w.run(t, "status")
	want := "error: database is locked\ncode: internal\nhelp[1]: \"this is a bug in wakewire"
	if code != exitError || !strings.HasPrefix(out, want) || errOut != "" {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

// An unknown flag is refused by name, before anything runs, with the flags
// the command does declare. --json is one global flag, so the list leaves it
// and --help out and says they are always allowed; a command with no flags
// of its own says so.
func TestAnUnknownFlagListsTheValidOnesButNeverJSONOrHelp(t *testing.T) {
	for cmd, want := range map[string]string{
		"pause":   "valid flags for `wakewire pause`: --for, --identity, --dry-run (--help and --json always allowed)",
		"status":  "valid flags for `wakewire status`: --session, --identity, --fields (--help and --json always allowed)",
		"paths":   "`wakewire paths` takes no flags but --help and --json",
		"version": "`wakewire version` takes no flags but --help and --json",
	} {
		w := newFakeWorld()
		code, out, _ := w.run(t, cmd, "--nonsense")
		if code != exitUsage || w.opens != 0 || !strings.Contains(out, "error: unknown flag --nonsense for `wakewire "+cmd+"`\ncode: usage\n") ||
			!strings.Contains(out, want) {
			t.Errorf("%s: exit %d, %d opens, lacks %q:\n%s", cmd, code, w.opens, want, out)
		}
	}
	// Every command takes --json, though only the root declares it.
	w := newFakeWorld()
	for _, args := range [][]string{{"paths"}, {"version"}, {"doctor"}, {"account", "list"}, {"adapter", "ack", "1"}, {"changes", "1"}} {
		if code, out, _ := w.run(t, append(args, "--json")...); code != exitOK {
			t.Errorf("%v --json: exit %d\n%s", args, code, out)
		}
	}
}

// An unknown command is refused as usage, with the commands there are, and
// a word after a command with subcommands is one too.
func TestAnUnknownCommandListsTheCommands(t *testing.T) {
	for _, c := range []struct {
		args          []string
		message, hint string
	}{
		{[]string{"frob"}, "unknown command \"frob\" for `wakewire`", "commands of `wakewire`: register, unregister, pause"},
		{[]string{"adapter", "bogus"}, "unknown command \"bogus\" for `wakewire adapter`", "commands of `wakewire adapter`: hello, next, ack, ended"},
		{[]string{"account", "nope"}, "unknown command \"nope\" for `wakewire account`", "commands of `wakewire account`: add, list, rename, remove"},
	} {
		w := newFakeWorld()
		code, out, _ := w.run(t, append(c.args, "--json")...)
		e, _ := object(t, out)["error"].(map[string]any)
		if code != exitUsage || e["code"] != "usage" || e["message"] != c.message || !strings.HasPrefix(e["hint"].(string), c.hint) {
			t.Errorf("%v: exit %d\n%s", c.args, code, out)
		}
	}
}

// A flag a command cannot run without is usage when it is missing.
func TestAMissingRequiredFlagIsUsage(t *testing.T) {
	for _, c := range []struct {
		args    []string
		message string
	}{{[]string{"adapter", "next"}, "missing required flag --session"}, {[]string{"account", "add", "w"}, "--host is required"}} {
		w := newFakeWorld()
		code, out, _ := w.run(t, append(c.args, "--json")...)
		e, _ := object(t, out)["error"].(map[string]any)
		if code != exitUsage || e["code"] != "usage" || e["message"] != c.message || e["hint"] == "" || w.opens != 0 {
			t.Errorf("%v: exit %d\n%s", c.args, code, out)
		}
	}
}

// wakewire has no help command: --help is the one way to help. `help`,
// whatever follows it, is refused as usage on stdout, naming --help, with
// nothing on stderr and one object under --json.
func TestHelpIsNotACommandAndNamesTheHelpFlag(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"help", "status"}, {"help", "bogus"}} {
		w := newFakeWorld()
		code, out, errOut := w.run(t, args...)
		if code != exitUsage || errOut != "" || !strings.Contains(out, "code: usage\n") ||
			!strings.Contains(out, "`wakewire <command> --help`") {
			t.Errorf("%v: exit %d stderr %q\n%s", args, code, errOut, out)
		}
	}
	// A flag is read before the command is, so an unknown one is named first.
	w := newFakeWorld()
	if code, out, errOut := w.run(t, "help", "--nonsense"); code != exitUsage || errOut != "" || !strings.Contains(out, "unknown flag --nonsense") {
		t.Errorf("help --nonsense: exit %d stderr %q\n%s", code, errOut, out)
	}
	code, out, errOut := w.run(t, "help", "status", "--json")
	if code != exitUsage || errOut != "" || errorCode(t, out) != "usage" || strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("help status --json: exit %d stderr %q\n%s", code, errOut, out)
	}
}

// --help lists --json once, in the one global line, not once more on every command.
func TestHelpListsJSONOnceInTheGlobalLine(t *testing.T) {
	for _, args := range [][]string{{"account", "rename", "--help"}, {"status", "--help"}, {"adapter", "next", "--help"}, {"--help"}, {"adapter"}} {
		w := newFakeWorld()
		code, out, _ := w.run(t, args...)
		// The flag's own line, not the examples that mention it.
		if n := strings.Count(out, "print exactly one JSON object on stdout"); code != exitOK || n != 1 || !strings.Contains(out, "\nGlobal: --json  print exactly one JSON object on stdout; --help, -h  show help\n") {
			t.Errorf("%v: exit %d, the flag listed %d times\n%s", args, code, n, out)
		}
	}
}

// The help of every account and adapter command says what it does, with an
// example and its exit codes; an account command names related ones, and an
// adapter command with --session says it is required.
func TestEveryAccountAndAdapterCommandHelpHasExamplesAndExitCodes(t *testing.T) {
	for _, c := range []struct {
		command string
		extra   string
	}{
		{"account add", "Related:"}, {"account list", "Related:"}, {"account rename", "Related:"}, {"account remove", "Related:"},
		{"adapter hello", "(required)"}, {"adapter next", "(required)"}, {"adapter ack", ""}, {"adapter ended", "(required)"},
	} {
		w := newFakeWorld()
		code, out, errOut := w.run(t, append(strings.Fields(c.command), "--help")...)
		if code != exitOK || errOut != "" {
			t.Errorf("%s --help: exit %d stderr %q", c.command, code, errOut)
		}
		for _, want := range []string{"Examples:", "wakewire " + c.command, "Exit codes:", c.extra} {
			if !strings.Contains(out, want) {
				t.Errorf("%s --help lacks %q:\n%s", c.command, want, out)
			}
		}
	}
}
