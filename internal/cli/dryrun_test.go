package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/wiring"
)

// runReal runs one command line against the real wiring over $WAKEWIRE_HOME.
func runReal(args ...string) int {
	var out, errOut bytes.Buffer
	return Run(context.Background(), append([]string{"wakewire"}, args...), wiring.ReadBuild("test", "c"), strings.NewReader(""), &out, &errOut)
}

// A dry run changes nothing, and on a machine where wakewire has never run
// that includes making its folder or state.db.
func TestDryRunsCreateNothingWhereWakewireNeverRan(t *testing.T) {
	for name, home := range map[string]func(*testing.T) string{
		"an empty home":  func(t *testing.T) string { return t.TempDir() },
		"no home at all": func(t *testing.T) string { return filepath.Join(t.TempDir(), "never-made") },
		"a home with a config": func(t *testing.T) string {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[poll]\ninterval = \"1m\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return dir
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := home(t)
			t.Setenv("WAKEWIRE_HOME", dir)
			t.Setenv("CLAUDE_CODE_SESSION_ID", "s1")
			t.Setenv("CLAUDE_PID", "1")
			before := listing(t, dir)
			for _, args := range [][]string{
				{"prune", "--dry-run"},
				{"unregister", "github.com/o/x", "--dry-run"},
				{"pause", "github.com/o/x", "--dry-run"},
				{"resume", "github.com/o/x", "--dry-run"},
				{"interval", "github.com/o/x", "1m", "--dry-run"},
				{"register", "--dry-run", "--repo", "github.com/o/x", "--tracker", "github-issues",
					"--label", "spec=spec", "--label", "ready=ready", "--label", "in-progress=wip", "--label", "needs-input=input"},
			} {
				runReal(args...)
				if after := listing(t, dir); after != before {
					t.Fatalf("%v changed the home:\nbefore:\n%s\nafter:\n%s", args, before, after)
				}
			}
		})
	}
}

// listing names every file and folder under dir, or says it is missing.
func listing(t *testing.T, dir string) string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		names = append(names, p)
		return nil
	})
	if os.IsNotExist(err) {
		return "(missing)"
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(names, "\n")
}

func TestDryRunsAnswerOverAnEmptyState(t *testing.T) {
	t.Setenv("WAKEWIRE_HOME", filepath.Join(t.TempDir(), "never-made"))
	if code := runReal("prune", "--dry-run"); code != exitOK {
		t.Errorf("prune --dry-run exited %d", code)
	}
	if code := runReal("unregister", "github.com/o/x", "--dry-run"); code != exitOK {
		t.Errorf("unregister --dry-run exited %d: nothing to forget is not an error", code)
	}
	if code := runReal("pause", "github.com/o/x", "--dry-run"); code != exitRefused {
		t.Errorf("pause --dry-run of a watch that does not exist exited %d", code)
	}
}
