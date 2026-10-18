package credfile

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/adapters/home"
)

func newVault(t *testing.T) (*Vault, home.Paths) {
	t.Helper()
	dir := t.TempDir()
	paths, err := home.Resolve(func(k string) string {
		if k == "WAKEWIRE_HOME" {
			return dir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(paths), paths
}

func TestPutTokenNamesDeleteRoundTrip(t *testing.T) {
	v, paths := newVault(t)
	if _, found, err := v.Token("a"); found || err != nil {
		t.Fatalf("a missing file holds nothing: %v %v", found, err)
	}
	odd := "pa\"ss\\w\nord"
	for name, tok := range map[string]string{"b": "tok-b", "a": odd, "needs.quotes": "tok-q"} {
		if err := v.Put(name, tok); err != nil {
			t.Fatal(err)
		}
	}
	if got, found, err := v.Token("a"); err != nil || !found || got != odd {
		t.Fatalf("%q %v %v", got, found, err)
	}
	names, _ := v.Names()
	if strings.Join(names, ",") != "a,b,needs.quotes" {
		t.Fatalf("names %v", names)
	}
	if err := v.Delete("b"); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete("b"); err != nil {
		t.Fatal("deleting twice is not an error")
	}
	if _, found, _ := v.Token("b"); found {
		t.Fatal("b survived")
	}
	info, err := os.Stat(paths.Credentials)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", info, err)
	}
}

func TestOverwritingKeepsTheModeAt600(t *testing.T) {
	v, paths := newVault(t)
	if err := os.WriteFile(paths.Credentials, []byte("[a]\ntoken = \"x\"\n"), 0o644); err != nil { //nolint:gosec // the test needs a file that is too open
		t.Fatal(err)
	}
	if err := v.Put("a", "y"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(paths.Credentials); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestAnUnparsableFileIsConfigInvalidAndNeverQuotesATokenOrChangesTheFile(t *testing.T) {
	v, paths := newVault(t)
	broken := "[a]\ntoken = \"sekrit-token-value\n"
	if err := os.WriteFile(paths.Credentials, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{v.Put("b", "x"), func() error { _, _, e := v.Token("a"); return e }()} {
		var c interface {
			Code() string
			Hint() string
		}
		if !errors.As(err, &c) || c.Code() != "config_invalid" || c.Hint() == "" {
			t.Fatalf("got %v", err)
		}
		if strings.Contains(err.Error()+c.Hint(), "sekrit") {
			t.Fatalf("the error quotes the token: %v", err)
		}
	}
	if got, _ := os.ReadFile(paths.Credentials); string(got) != broken {
		t.Fatal("the broken file was rewritten")
	}
}
