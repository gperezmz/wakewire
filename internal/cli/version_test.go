package cli

import (
	"strings"
	"testing"
)

// -v, -V and --version alone print the bare version, one line, and nothing
// else: no TOON, no JSON, nothing on stderr.
func TestVersionFlagsPrintTheBareVersion(t *testing.T) {
	for _, flag := range []string{"-v", "-V", "--version"} {
		code, out, errOut := newFakeWorld().run(t, flag)
		if code != exitOK || out != "test\n" || errOut != "" {
			t.Errorf("%s: exit %d stdout %q stderr %q", flag, code, out, errOut)
		}
	}
}

// The version command is a TOON object by default; its --json is pinned by
// the golden file.
func TestVersionCommandPrintsTOON(t *testing.T) {
	code, out, _ := newFakeWorld().run(t, "version")
	if want := "version: test\ncommit: c\nprotocol: 2\n"; code != exitOK || out != want {
		t.Errorf("exit %d stdout %q, want %q", code, out, want)
	}
}

// A version flag next to anything else is not the fast path: it is an
// unknown flag of that command, and the hint says it stands alone.
func TestAVersionFlagBesideACommandIsAnUnknownFlagThatStandsAlone(t *testing.T) {
	for _, args := range [][]string{{"status", "--version"}, {"status", "-v"}, {"version", "--version"}} {
		code, out, _ := newFakeWorld().run(t, args...)
		if code != exitUsage || strings.HasPrefix(out, "test") || !strings.Contains(out, "unknown flag "+args[1]+" for `wakewire "+args[0]+"`") ||
			!strings.Contains(out, "--version stands alone, as `wakewire --version`") {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
}
