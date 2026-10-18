//go:build record || contract

package gitlab_test

import (
	"os"
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/adapters/home"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
	"github.com/gperezmz/wakewire/v2/internal/wiring"
)

// realSandbox is the real project the record and contract tests use, named
// by the environment so that the repository holds no real name:
//
//	WAKEWIRE_GITLAB_SANDBOX=<host>/<owner>/<project>
func realSandbox(t *testing.T) domain.Repo {
	t.Helper()
	v := os.Getenv("WAKEWIRE_GITLAB_SANDBOX")
	if v == "" {
		t.Skip("set WAKEWIRE_GITLAB_SANDBOX to <host>/<owner>/<project>")
	}
	r, err := domain.ParseRepo(v)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// tokenOf answers the read token of the account that reads host's owner,
// through the account service: never from a flag, and never printed.
func tokenOf(t *testing.T, host, owner string) string {
	t.Helper()
	paths, err := home.Resolve(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := wiring.NewAccounts(paths, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := svc.ForRepo(host, owner)
	if err != nil {
		t.Skipf("no account reads %s/%s yet, so there is no token to read with: %v", host, owner, err)
	}
	return cred.Token
}
