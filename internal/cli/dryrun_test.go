package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A dry run changes nothing, and on a machine where wakewire has never run
// that includes making its folder or state.db. Over that empty state each
// still answers: nothing to forget is no error, no watch to pause is a refusal.
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
			for _, c := range []struct {
				args []string
				exit int // -1: whatever it answers
			}{
				{[]string{"prune", "--dry-run"}, exitOK},
				{[]string{"unregister", "github.com/o/x", "--dry-run"}, exitOK},
				{[]string{"pause", "github.com/o/x", "--dry-run"}, exitError},
				{[]string{"resume", "github.com/o/x", "--dry-run"}, -1},
				{[]string{"interval", "github.com/o/x", "1m", "--dry-run"}, -1},
				{[]string{"register", "--dry-run", "--repo", "github.com/o/x", "--tracker", "github-issues",
					"--label", "spec=spec", "--label", "ready=ready", "--label", "in-progress=wip", "--label", "needs-input=input"}, -1},
			} {
				if code, out, _ := runReal(c.args...); c.exit >= 0 && code != c.exit {
					t.Errorf("%v: exit %d, want %d\n%s", c.args, code, c.exit, out)
				}
				if after := listing(t, dir); after != before {
					t.Fatalf("%v changed the home:\nbefore:\n%s\nafter:\n%s", c.args, before, after)
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
