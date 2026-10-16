package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
