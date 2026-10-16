//go:build record || contract

package jira_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/adapters/home"
	"github.com/gperezmz/wakewire/internal/wiring"
)

// The real sandbox: the KAN project of the Jira Cloud site that
// WAKEWIRE_JIRA_SANDBOX names (as <site>.atlassian.net), which the user
// designated for these tests, so the repository names no site. Its
// credential is read through the account service, from the account stored
// for the site; the token is never printed, logged or written.
func realSite() string { return os.Getenv("WAKEWIRE_JIRA_SANDBOX") }

// realSiteTag is the site's own name, which the recordings replace.
func realSiteTag() string { site, _, _ := strings.Cut(realSite(), "."); return site }

// sandbox reads the sandbox with a plain HTTP client. Writes live in
// record_test.go and exist only in tests: wakewire itself only reads.
type sandbox struct {
	t     *testing.T
	email string
	token string
	hc    *http.Client
	// Who the credential belongs to.
	accountID   string
	displayName string
}

// newSandbox skips the test when no account is stored for the site.
func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	if realSite() == "" {
		t.Skip("set WAKEWIRE_JIRA_SANDBOX to the sandbox's <site>.atlassian.net")
	}
	paths, err := home.Resolve(os.Getenv)
	if err != nil {
		t.Skip("no home folder: ", err)
	}
	accts, err := wiring.NewAccounts(paths, os.Getenv)
	if err != nil {
		t.Skip("accounts: ", err)
	}
	cred, err := accts.ForSite(realSite())
	if err != nil {
		t.Skipf("no account for %s yet: %v", realSite(), err)
	}
	sb := &sandbox{t: t, email: cred.Email, token: cred.Token, hc: &http.Client{Timeout: 30 * time.Second}}
	var me struct {
		AccountID   string `json:"accountId"`
		DisplayName string `json:"displayName"`
	}
	sb.get("/rest/api/3/myself", &me)
	sb.accountID, sb.displayName = me.AccountID, me.DisplayName
	return sb
}

// send makes one request and answers the body. A failure names the
// request and Jira's answer, never the credential.
func (sb *sandbox) send(method, path string, body any) []byte {
	sb.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			sb.t.Fatal(err)
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, "https://"+realSite()+path, rd)
	if err != nil {
		sb.t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(sb.email, sb.token)
	resp, err := sb.hc.Do(req)
	if err != nil {
		sb.t.Fatalf("%s %s: no answer", method, path)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		sb.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, out)
	}
	return out
}

func (sb *sandbox) get(path string, into any) {
	sb.t.Helper()
	b := sb.send(http.MethodGet, path, nil)
	if into != nil {
		if err := json.Unmarshal(b, into); err != nil {
			sb.t.Fatalf("GET %s: %v", path, err)
		}
	}
}

// inPartition says whether key is among the search's results for the
// identity's partition, without reconcileIssues: what a search shows alone.
func (sb *sandbox) inPartition(key string) bool {
	sb.t.Helper()
	jql := fmt.Sprintf(`project = "KAN" AND labels = "type::spec" AND (assignee = %q OR (reporter = %q AND assignee is EMPTY))`, sb.accountID, sb.accountID)
	var page struct {
		Issues []struct {
			Key string `json:"key"`
		} `json:"issues"`
	}
	b := sb.send(http.MethodPost, "/rest/api/3/search/jql", map[string]any{"jql": jql, "fields": []string{"summary"}, "maxResults": 100})
	if err := json.Unmarshal(b, &page); err != nil {
		sb.t.Fatal(err)
	}
	for _, i := range page.Issues {
		if i.Key == key {
			return true
		}
	}
	return false
}

// waitIndexed waits for the search index to show key, up to two minutes.
func (sb *sandbox) waitIndexed(key string) time.Duration {
	sb.t.Helper()
	start := time.Now()
	for time.Since(start) < 2*time.Minute {
		if sb.inPartition(key) {
			return time.Since(start)
		}
		time.Sleep(2 * time.Second)
	}
	sb.t.Fatalf("%s did not reach the search index within two minutes", key)
	return 0
}
