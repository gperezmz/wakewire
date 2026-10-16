//go:build record

package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/adapters/contract"
)

// Records the scenario against the real sandbox, making real changes on
// it with gh and git between polls:
//
//	GH_TOKEN=… WAKEWIRE_SANDBOX_CLONE=~/scratch/wakewire-sandbox \
//	  go test -tags record -run TestRecordScenario ./internal/watch/adapters/github/
//
// It writes testdata/contract.json, scrubbed, and testdata/expect.json.

const realRepo = "github.com/gperezmz/wakewire-sandbox"

func sh(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func gh(t *testing.T, args ...string) string { return sh(t, "", "gh", args...) }

// scrubber replaces the sandbox's owner, the numeric ids of the owner and of
// the repository, and the names and email git and GitHub hold for the person
// recording. The ids come from the API at run time, never from the code.
func scrubber(t *testing.T) contract.Scrubber {
	replace := [][2]string{{"gperezmz", "wwuser"}}
	for _, v := range []string{gh(t, "api", "user", "--jq", ".name"), sh(t, "", "git", "config", "user.name")} {
		if v != "" && v != "gperezmz" {
			replace = append(replace, [2]string{v, "Wakewire User"})
		}
	}
	return contract.Scrubber{
		Replace: replace,
		Numbers: map[string]string{
			gh(t, "api", "user", "--jq", ".id"):                                               "1001",
			gh(t, "api", "repos/"+strings.TrimPrefix(realRepo, "github.com/"), "--jq", ".id"): "1002",
		},
		KeepRequest:  []string{"If-None-Match"},
		KeepResponse: []string{"Etag", "Link", "Date", "X-Ratelimit-Limit", "X-Ratelimit-Remaining", "X-Ratelimit-Reset"},
		// Fields that name a person the client never reads.
		Drop: []string{"avatar_url", "gravatar_id", "node_id"},
	}
}

// TestRescrub applies the scrubber again to a recording already written.
func TestRescrub(t *testing.T) {
	c, err := contract.Load(contractFile)
	if err != nil {
		t.Skip(err)
	}
	s := scrubber(t)
	c = s.Contract(c)
	if err := c.Save(contractFile); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(expectFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expectFile, []byte(s.Text(string(b))), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRecordScenario(t *testing.T) {
	time.Local = time.UTC
	token, clone := os.Getenv("GH_TOKEN"), os.Getenv("WAKEWIRE_SANDBOX_CLONE")
	if token == "" || clone == "" {
		t.Skip("set GH_TOKEN and WAKEWIRE_SANDBOX_CLONE")
	}
	rec := &contract.Recorder{Next: http.DefaultTransport, Scrub: scrubber(t)}
	r := newRunner(t, realRepo, "gperezmz", API, rec, token)
	var steps []step
	poll := func(what string) {
		time.Sleep(4 * time.Second) // GitHub's lists catch up within seconds
		rec.Name = what
		w := r.poll()
		t.Logf("%s:\n%s", what, w)
		steps = append(steps, step{Name: what, Wake: w})
	}
	repo := "gperezmz/wakewire-sandbox"
	poll("first poll: what is there is no change")

	url := gh(t, "issue", "create", "--repo", repo, "--title", "Spec: export the report", "--body", "Export as CSV.",
		"--label", "type::spec", "--assignee", "gperezmz")
	n := url[strings.LastIndex(url, "/")+1:]
	poll("a new spec in the partition")

	gh(t, "issue", "edit", n, "--repo", repo, "--add-label", "ready-for-dev,in-progress")
	gh(t, "issue", "comment", n, "--repo", repo, "--body", "Please also support XLSX.")
	gh(t, "issue", "comment", n, "--repo", repo, "--body", "Started on it.\n\n<kbd>🤖 unattendish agent</kbd>")
	poll("ready label, agent label, a comment and an echo")

	gh(t, "issue", "edit", n, "--repo", repo, "--body", "Export as CSV.\n\nAnd XLSX.")
	poll("a description edit, held")
	poll("the edit holds still: reported")
	poll("nothing moved: every list answers 304")

	branch := fmt.Sprintf("ww-%d", time.Now().Unix())
	sh(t, clone, "git", "checkout", "-q", "-B", branch, "origin/main")
	if err := os.WriteFile(filepath.Join(clone, "report.txt"), []byte("csv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sh(t, clone, "git", "add", "report.txt")
	sh(t, clone, "git", "commit", "-qm", "feat: export as CSV")
	sh(t, clone, "git", "push", "-q", "origin", branch)
	pr := gh(t, "pr", "create", "--repo", repo, "--head", branch, "--title", "Export as CSV", "--body", "Closes #"+n)
	p := pr[strings.LastIndex(pr, "/")+1:]
	poll("a new pull request of the identity's")

	head := sh(t, clone, "git", "rev-parse", "HEAD")
	gh(t, "pr", "comment", p, "--repo", repo, "--body", "Looks good so far.")
	gh(t, "api", "-X", "POST", "repos/"+repo+"/pulls/"+p+"/comments", "-f", "body=Name this file export.csv.",
		"-f", "commit_id="+head, "-f", "path=report.txt", "-F", "line=1", "-f", "side=RIGHT")
	gh(t, "api", "-X", "POST", "repos/"+repo+"/pulls/"+p+"/reviews", "-f", "event=COMMENT", "-f", "body=One question inline.")
	poll("a comment, an inline comment and a review")

	if err := os.WriteFile(filepath.Join(clone, "report.txt"), []byte("csv\nxlsx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sh(t, clone, "git", "commit", "-qam", "feat: export as XLSX", "--author", "Ada Reviewer <ada@example.com>")
	sh(t, clone, "git", "push", "-q", "origin", branch)
	poll("a push with someone else's commit")

	gh(t, "pr", "merge", p, "--repo", repo, "--merge")
	poll("merged")

	gh(t, "issue", "close", n, "--repo", repo)
	poll("the spec closed")
	sh(t, clone, "git", "checkout", "-q", "main")

	c := rec.Scrub.Contract(rec.Contract("GitHub", "Recorded by record_test.go against "+realRepo+" on "+time.Now().UTC().Format("2006-01-02")))
	if err := c.Save(contractFile); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(steps, "", "  ")
	scrub := rec.Scrub.Text(string(b))
	if err := os.WriteFile(expectFile, []byte(scrub+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
