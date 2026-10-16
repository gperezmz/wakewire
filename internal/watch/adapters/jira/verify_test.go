//go:build contract

package jira_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/adapters/contract"
)

// The provider side: the recorded requests sent to Jira now, failing where
// a field the recording holds is gone or has changed type. It reads the
// credential of the account stored for the site and skips without one.
//
//	go test -tags contract -run TestJiraStillKeepsTheContract ./internal/watch/adapters/jira/
func TestJiraStillKeepsTheContract(t *testing.T) {
	sb := newSandbox(t)
	c, err := contract.Load(contractFile)
	if err != nil {
		t.Fatal(err)
	}
	restore := strings.NewReplacer(account, sb.accountID, "wwsite", realSiteTag).Replace
	diffs, err := contract.Live(c, "https://"+realSite, http.DefaultClient, func(r *http.Request) {
		r.SetBasicAuth(sb.email, sb.token)
	}, restore)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range diffs {
		t.Errorf("%s: %s", c.Interactions[i], strings.Join(d, "; "))
	}
}
