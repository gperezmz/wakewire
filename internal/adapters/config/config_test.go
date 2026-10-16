package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type coded interface {
	Code() string
	Hint() string
}

func wantInvalid(t *testing.T, err error, in string) {
	t.Helper()
	var c coded
	if !errors.As(err, &c) || c.Code() != "config_invalid" || c.Hint() == "" {
		t.Fatalf("want config_invalid with a hint, got %v", err)
	}
	if !strings.Contains(err.Error(), in) {
		t.Fatalf("error %q lacks %q", err, in)
	}
}

func TestMissingFileIsAllDefaults(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Poll.Interval != "2m" || f.Echo.Marker != "🤖 unattendish agent" || len(f.Accounts) != 0 {
		t.Fatalf("got %+v", f)
	}
	if f.Source.Heading != "Forge and tracker" || f.Source.Labels.InProgress != "in progress" || f.Source.Table.Value != "Label" ||
		len(f.Source.Files) != 2 || f.Source.Tracker != "Tracker:" {
		t.Fatalf("source defaults: %+v", f.Source)
	}
}

func TestAConfigOverridesOnlyTheKeysItSets(t *testing.T) {
	f, err := Load(put(t, `
[source]
heading = "Where things live"

[source.labels]
in_progress = "doing"

[poll]
interval = "45s"
`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Source.Heading != "Where things live" || f.Source.Labels.InProgress != "doing" || f.Poll.Interval != "45s" {
		t.Fatalf("overrides lost: %+v", f)
	}
	if f.Source.Labels.NeedsInput != "needs input" || f.Source.Tracker != "Tracker:" || f.Echo.Marker == "" {
		t.Fatalf("defaults lost: %+v", f)
	}
}

func TestTheDocumentedExampleLoads(t *testing.T) {
	f, err := Load(put(t, `
[[accounts]]
name   = "work-github"
host   = "github.com"
owners = ["acme"]
token  = "gh:jdoe"
user   = "jdoe"

[[accounts]]
name   = "work-jira"
host   = "acme.atlassian.net"
email  = "jdoe@example.com"
token  = "stored"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Accounts) != 2 || f.Accounts[0].Owners[0] != "acme" || f.Accounts[0].User != "jdoe" ||
		f.Accounts[1].Email != "jdoe@example.com" || f.Accounts[1].Token != "stored" {
		t.Fatalf("got %+v", f.Accounts)
	}
}

func TestInvalidConfigurationsAreConfigInvalidWithAHint(t *testing.T) {
	cases := map[string]struct{ text, in string }{
		"parse error": {"[[accounts]\nname = 1", "line"},
		"duplicate name": {`
[[accounts]]
name = "a"
host = "github.com"
owners = ["x"]
token = "stored"
[[accounts]]
name = "a"
host = "gitlab.com"
owners = ["y"]
token = "stored"`, `two accounts are named "a"`},
		"two accounts one host and owner": {`
[[accounts]]
name = "a"
host = "github.com"
owners = ["X", "z"]
token = "stored"
[[accounts]]
name = "b"
host = "GitHub.com"
owners = ["x"]
token = "stored"`, "both cover github.com/x"},
		"two jira accounts one site": {`
[[accounts]]
name = "a"
host = "s.atlassian.net"
email = "a@b.c"
token = "stored"
[[accounts]]
name = "b"
host = "s.atlassian.net"
email = "a@b.c"
token = "stored"`, "both cover s.atlassian.net"},
		"no host":        {"[[accounts]]\nname = \"a\"\ntoken = \"stored\"", "no host"},
		"short interval": {"[poll]\ninterval = \"10s\"", "interval"},
		"odd interval":   {"[poll]\ninterval = \"often\"", "interval"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(put(t, c.text))
			wantInvalid(t, err, c.in)
		})
	}
}

func TestAParseErrorNeverQuotesTheSource(t *testing.T) {
	_, err := Load(put(t, "token = \"sekrit-value\nmore"))
	if err == nil {
		t.Fatal("want an error")
	}
	var c coded
	errors.As(err, &c)
	if strings.Contains(err.Error()+c.Hint(), "sekrit") {
		t.Fatalf("error quotes the file: %v", err)
	}
}

func TestAParseErrorNeverQuotesATokenPrefix(t *testing.T) {
	for _, text := range []string{"[[accounts]]\ntoken = glpat-SECRETVALUE0123\n", "token = ghp_SECRETVALUE0123\n"} {
		_, err := Load(put(t, text))
		if err == nil {
			t.Fatal("want an error")
		}
		var c coded
		errors.As(err, &c)
		if strings.Contains(err.Error()+c.Hint(), "glpat") || strings.Contains(err.Error()+c.Hint(), "ghp_") || strings.Contains(err.Error()+c.Hint(), "SECRET") {
			t.Fatalf("error quotes the file: %v %s", err, c.Hint())
		}
	}
}

func TestAnIntervalMayBeGivenInDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[poll]\ninterval = \"1d\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}
