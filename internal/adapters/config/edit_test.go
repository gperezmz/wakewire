package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# my accounts, shared in my dotfiles
[poll]
interval = "2m"   # keep it slow

# the work forge
[[accounts]]
name   = "work-github"   # work
host   = "github.com"
owners = [
  "acme",   # the org
]
token  = "gh:jdoe"

# the personal one
[[accounts]]
name   = "personal-github"
host   = "github.com"
owners = ["gperezmz"]
token  = "gh:gperezmz"

[echo]
marker = "🤖 unattendish agent"
`

func slurp(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAppendKeepsEverythingAndAddsABlock(t *testing.T) {
	path := put(t, sample)
	err := AppendAccount(path, Account{Name: "work-jira", Host: "acme.atlassian.net", Email: "g@n.com", Token: "stored"})
	if err != nil {
		t.Fatal(err)
	}
	got := slurp(t, path)
	if !strings.HasPrefix(got, sample+"\n[[accounts]]\nname   = \"work-jira\"\nhost   = \"acme.atlassian.net\"\nemail  = \"g@n.com\"\ntoken  = \"stored\"\n") {
		t.Fatalf("got:\n%s", got)
	}
	f, err := Load(path)
	if err != nil || len(f.Accounts) != 3 || f.Accounts[2].Name != "work-jira" {
		t.Fatalf("%v %+v", err, f.Accounts)
	}
}

func TestAppendCreatesAMissingFileWithMode644(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := AppendAccount(path, Account{Name: "a", Host: "gitlab.x.com", Owners: []string{"p", "q"}, Token: "env:X", User: "me"}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", info.Mode())
	}
	f, err := Load(path)
	if err != nil || f.Accounts[0].Owners[1] != "q" || f.Accounts[0].User != "me" {
		t.Fatalf("%v %+v", err, f.Accounts)
	}
}

func TestAppendQuotesWhatNeedsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := AppendAccount(path, Account{Name: `we"ird\name`, Host: "h.com", Owners: []string{"o"}, Token: "stored"}); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil || f.Accounts[0].Name != `we"ird\name` {
		t.Fatalf("%v %+v", err, f.Accounts)
	}
}

func TestAppendRefusesABrokenFileAndLeavesItAlone(t *testing.T) {
	path := put(t, "[[accounts]\n")
	err := AppendAccount(path, Account{Name: "a", Host: "h", Owners: []string{"o"}, Token: "stored"})
	wantInvalid(t, err, "does not parse")
	if slurp(t, path) != "[[accounts]\n" {
		t.Fatal("the file changed")
	}
}

func TestRenameRewritesOnlyTheNameLine(t *testing.T) {
	path := put(t, sample)
	if err := RenameAccount(path, "work-github", "work-github"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sample, `name   = "work-github"   # work`, `name   = "work-github"   # work`, 1)
	if got := slurp(t, path); got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenameOfAnAbsentAccountIsAnError(t *testing.T) {
	path := put(t, sample)
	wantInvalid(t, RenameAccount(path, "nope", "x"), "nope")
}

func TestRemoveDeletesTheBlockItsCommentAndOneBlankLine(t *testing.T) {
	path := put(t, sample)
	if err := RemoveAccount(path, "work-github"); err != nil {
		t.Fatal(err)
	}
	want := `# my accounts, shared in my dotfiles
[poll]
interval = "2m"   # keep it slow

# the personal one
[[accounts]]
name   = "personal-github"
host   = "github.com"
owners = ["gperezmz"]
token  = "gh:gperezmz"

[echo]
marker = "🤖 unattendish agent"
`
	if got := slurp(t, path); got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRemoveTheLastBlockAtTheEndOfTheFile(t *testing.T) {
	path := put(t, "[poll]\ninterval = \"1m\"\n\n[[accounts]]\nname = \"a\"\nhost = \"h\"\nowners = [\"o\"]\ntoken = \"stored\"\n")
	if err := RemoveAccount(path, "a"); err != nil {
		t.Fatal(err)
	}
	if got := slurp(t, path); got != "[poll]\ninterval = \"1m\"\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRemoveTheOnlyBlockLeavesAnEmptyFile(t *testing.T) {
	path := put(t, "[[accounts]]\nname = \"a\"\nhost = \"h\"\nowners = [\"o\"]\ntoken = \"stored\"\n")
	if err := RemoveAccount(path, "a"); err != nil {
		t.Fatal(err)
	}
	if got := slurp(t, path); got != "" {
		t.Fatalf("got %q", got)
	}
}
