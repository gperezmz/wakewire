package gitrepo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	for raw, want := range map[string][3]string{
		"git@github.com-personal:gperezmz/wakewire.git":    {"github.com-personal", "gperezmz/wakewire", "ssh"},
		"https://github.com/o/r.git":                       {"github.com", "o/r", ""},
		"ssh://git@gitlab.example.com:2222/g/sub/proj.git": {"gitlab.example.com", "g/sub/proj", "ssh"},
		"https://user@gitlab.example.com/a/b":              {"gitlab.example.com", "a/b", ""},
	} {
		host, path, ssh, err := ParseRemote(raw)
		if err != nil || host != want[0] || path != want[1] || ssh != (want[2] == "ssh") {
			t.Errorf("%s: %s %s %v %v", raw, host, path, ssh, err)
		}
	}
	if _, _, _, err := ParseRemote("/local/path"); err == nil {
		t.Error("a local path parsed")
	}
}

// What is wrong with an origin has a code of its own, so a caller can tell
// a clone with no origin from an origin that is no forge.
func TestAnOriginThatIsMissingOrNoForgeHasItsOwnCode(t *testing.T) {
	code := func(err error) string {
		var c interface{ Code() string }
		if errors.As(err, &c) {
			return c.Code()
		}
		return ""
	}
	if _, _, _, err := ParseRemote("/local/path"); code(err) != "not_a_forge" {
		t.Errorf("a local path: %v", err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	dir := t.TempDir()
	git(dir, "init", "-q")
	if _, err := (Repos{}).Origin(context.Background(), dir); code(err) != "no_origin" {
		t.Errorf("a clone with no origin: %v", err)
	}
	// An origin that is a remote but names no <owner>/<repo> is no forge.
	git(dir, "remote", "add", "origin", "https://github.com/o/r.git")
	for _, remote := range []string{"https://github.com/solo", "git@github.com:solo.git", "https://github.com/"} {
		git(dir, "remote", "set-url", "origin", remote)
		_, err := (Repos{}).Origin(context.Background(), dir)
		if code(err) != "not_a_forge" || err == nil || !strings.Contains(err.Error(), remote) {
			t.Errorf("origin %s: %v", remote, err)
		}
	}
}

func TestOriginOfARealClone(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://github.com/o/r.git"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Repos{}
	repo, err := r.Origin(context.Background(), dir)
	if err != nil || repo.String() != "github.com/o/r" {
		t.Fatalf("%v %v", repo, err)
	}
	root, err := r.Root(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok, err := r.ReadFile(root, "AGENTS.md"); !ok || s != "x" || err != nil {
		t.Fatal(s, ok, err)
	}
	if _, ok, _ := r.ReadFile(root, "../../etc/passwd"); ok {
		t.Fatal("read outside the repository")
	}
}
