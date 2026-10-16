package ghcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH writes a script that answers like gh: it prints a token for the
// login it is asked for, unless one of the override variables reaches it.
func fakeGH(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	script := `#!/bin/sh
if [ -n "$GH_TOKEN$GITHUB_TOKEN$GH_ENTERPRISE_TOKEN$GITHUB_ENTERPRISE_TOKEN" ]; then echo "an override reached gh" >&2; exit 3; fi
if [ "$1 $2 $3 $5" != "auth token --hostname --user" ]; then echo "unexpected arguments: $*" >&2; exit 4; fi
case "$6" in
  known) echo "token-for-$4" ;;
  blank) ;;
  *) echo "no oauth token found for $4 account $6" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
	return path
}

func env() []string {
	return []string{"PATH=" + os.Getenv("PATH"), "GH_TOKEN=leak", "GITHUB_TOKEN=leak", "GH_ENTERPRISE_TOKEN=leak",
		"GITHUB_ENTERPRISE_TOKEN=leak", "KEEP=me"}
}

func TestTokenAsksForTheNamedLoginWithTheOverridesRemoved(t *testing.T) {
	c := CLI{Binary: fakeGH(t), Environ: env}
	got, err := c.Token("github.com", "known")
	if err != nil || got != "token-for-github.com" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestAFailureSaysWhatGHSaid(t *testing.T) {
	c := CLI{Binary: fakeGH(t), Environ: env}
	_, err := c.Token("github.com", "stranger")
	if err == nil || !strings.Contains(err.Error(), "no oauth token found for github.com account stranger") {
		t.Fatalf("got %v", err)
	}
	if _, err := c.Token("github.com", "blank"); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("got %v", err)
	}
}

func TestAMissingGHIsSaidPlainly(t *testing.T) {
	c := CLI{Binary: filepath.Join(t.TempDir(), "absent"), Environ: env}
	if _, err := c.Token("github.com", "known"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("got %v", err)
	}
}

func TestScrubKeepsEverythingElse(t *testing.T) {
	got := scrub(env())
	if len(got) != 2 || got[1] != "KEEP=me" {
		t.Fatalf("got %v", got)
	}
}
