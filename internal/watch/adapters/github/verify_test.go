//go:build contract

package github

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/adapters/contract"
)

// The provider side: the recorded requests sent to GitHub now, failing
// where a field the recording holds is gone or has changed type.
//
//	GH_TOKEN=… go test -tags contract -run TestGitHubStillKeepsTheContract ./internal/watch/adapters/github/
func TestGitHubStillKeepsTheContract(t *testing.T) {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		t.Skip("set GH_TOKEN")
	}
	c, err := contract.Load(contractFile)
	if err != nil {
		t.Fatal(err)
	}
	restore := strings.NewReplacer("wwuser", "gperezmz").Replace
	diffs, err := contract.Live(c, API, http.DefaultClient, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Accept", "application/vnd.github+json")
	}, restore)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range diffs {
		t.Errorf("%s: %s", c.Interactions[i], strings.Join(d, "; "))
	}
}
