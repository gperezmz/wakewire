package configstore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/account/domain"
	"github.com/gperezmz/wakewire/v2/internal/adapters/home"
)

func TestAddThenReadBackThenRenameThenRemove(t *testing.T) {
	paths, err := home.Resolve(func(k string) string {
		if k == "WAKEWIRE_HOME" {
			return filepath.Join(t.TempDir(), "fresh", ".wakewire")
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	s := New(paths)
	if got, err := s.Accounts(); err != nil || len(got) != 0 {
		t.Fatalf("a missing file has no accounts: %v %v", got, err)
	}
	a := domain.Account{Name: "work", Host: "github.com", Owners: []string{"ACME"}, User: "me", Token: domain.TokenSource{Kind: domain.GH, Arg: "me"}}
	if err := s.Add(a); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(paths.Home); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("home folder: %v %v", info, err)
	}
	got, err := s.Accounts()
	if err != nil || len(got) != 1 || !got[0].Same(a) {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.Rename("work", "job"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Accounts(); got[0].Name != "job" {
		t.Fatalf("%+v", got)
	}
	if err := s.Remove("job"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Accounts(); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestABadTokenSourceReadsAsTheZeroSource(t *testing.T) {
	dir := t.TempDir()
	paths, _ := home.Resolve(func(k string) string {
		if k == "WAKEWIRE_HOME" {
			return dir
		}
		return ""
	})
	text := "[[accounts]]\nname = \"a\"\nhost = \"github.com\"\nowners = [\"x\"]\ntoken = \"file:/tmp/x\"\n"
	if err := os.WriteFile(paths.Config, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := New(paths).Accounts()
	if err != nil || got[0].Token.Kind != "" || got[0].Validate() == nil {
		t.Fatalf("%+v %v", got, err)
	}
}
