//go:build contract

package gitlab_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/adapters/contract"
)

// The provider side: the recorded requests sent to GitLab now, failing
// where a field the recording holds is gone or has changed type. It reads
// with the account that reads the sandbox's owner, as wakewire does.
//
//	WAKEWIRE_GITLAB_SANDBOX=<host>/<owner>/<project> \
//	go test -tags contract -run TestGitLabStillKeepsTheContract ./internal/watch/adapters/gitlab/
func TestGitLabStillKeepsTheContract(t *testing.T) {
	repo := realSandbox(t)
	token := tokenOf(t, repo.Host, repo.Owner())
	c, err := contract.Load(contractFile)
	if err != nil {
		t.Fatal(err)
	}
	// The recording names the project by its path, one segment in GitLab's URLs.
	restore := strings.NewReplacer(
		"projects/wwuser/wakewire-sandbox", "projects/"+url.PathEscape(repo.Path),
		"wwuser", repo.Owner(),
		"gitlab.example.com", repo.Host,
	).Replace
	diffs, err := contract.Live(c, "https://"+repo.Host, http.DefaultClient, func(r *http.Request) {
		r.Header.Set("PRIVATE-TOKEN", token)
	}, restore)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range diffs {
		t.Errorf("%s: %s", c.Interactions[i], strings.Join(d, "; "))
	}
}
