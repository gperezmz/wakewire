//go:build record

package gitlab_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/adapters/contract"
)

// Records the scenario against a real sandbox project, making real changes
// on it with glab and git between polls. wakewire reads with the account
// that reads the project's owner; glab, logged in to the instance, writes.
//
//	WAKEWIRE_GITLAB_SANDBOX=<host>/<owner>/<project> WAKEWIRE_SANDBOX_CLONE=~/scratch/<clone> \
//	  go test -tags record -run TestRecordScenario ./internal/watch/adapters/gitlab/
//
// It writes testdata/contract.json, scrubbed, and testdata/expect.json.

func sh(t *testing.T, dir, stdin, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s: %v\n%s%s", name, strings.Join(args, " "), err, out.String(), errs.String())
	}
	return strings.TrimSpace(out.String())
}

// api calls the instance's REST API as the person recording, with glab.
type api struct {
	t    *testing.T
	host string
	proj string // the project's API path
}

// do sends a JSON body (nil for none) and answers the decoded JSON.
func (a api) do(method, path string, body any) map[string]any {
	a.t.Helper()
	args := []string{"api", "--hostname", a.host, "-X", method, path}
	in := ""
	if body != nil {
		b, _ := json.Marshal(body)
		args = append(args, "-H", "Content-Type: application/json", "--input", "-")
		in = string(b)
	}
	out := sh(a.t, "", in, "glab", args...)
	var m map[string]any
	_ = json.Unmarshal([]byte(out), &m)
	return m
}

func num(m map[string]any, key string) string { return fmt.Sprint(int(m[key].(float64))) }

// scrubber replaces the sandbox's owner, host and the names the instance
// and git hold for the person recording.
func scrubber(t *testing.T, a api, clone string) contract.Scrubber {
	replace := [][2]string{{realSandbox(t).Owner(), "wwuser"}, {a.host, "gitlab.example.com"}}
	me := a.do("GET", "user", nil)
	for _, v := range []string{fmt.Sprint(me["name"]), sh(t, clone, "", "git", "config", "user.name")} {
		if v != "" && v != realSandbox(t).Owner() {
			replace = append(replace, [2]string{v, "Wakewire User"})
		}
	}
	return contract.Scrubber{
		Replace:      replace,
		KeepRequest:  []string{"If-None-Match"},
		KeepResponse: []string{"Etag", "Date", "RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset", "RateLimit-ResetTime"},
		// Fields that name or place a person and that the client never reads.
		Drop: []string{"avatar_url", "identities", "last_sign_in_at", "current_sign_in_at", "confirmed_at", "last_activity_on", "local_time"},
	}
}

// TestRescrub applies the scrubber again to a recording already written.
func TestRescrub(t *testing.T) {
	c, err := contract.Load(contractFile)
	if err != nil {
		t.Skip(err)
	}
	repo := realSandbox(t)
	s := scrubber(t, api{t: t, host: repo.Host}, os.Getenv("WAKEWIRE_SANDBOX_CLONE"))
	c = s.Contract(c)
	if err := c.Save(contractFile); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(expectFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expectFile, []byte(s.Text(string(b))), 0o600); err != nil { //nolint:gosec // the recorder writes its own testdata and the sandbox clone it was told
		t.Fatal(err)
	}
}

func TestRecordScenario(t *testing.T) {
	time.Local = time.UTC
	repo, clone := realSandbox(t), os.Getenv("WAKEWIRE_SANDBOX_CLONE")
	if clone == "" {
		t.Skip("set WAKEWIRE_SANDBOX_CLONE")
	}
	token := tokenOf(t, repo.Host, repo.Owner())
	a := api{t: t, host: repo.Host}
	project := a.do("GET", "projects/"+url.PathEscape(repo.Path), nil)
	a.proj = "projects/" + num(project, "id")
	me := a.do("GET", "user", nil)
	identity, myID := fmt.Sprint(me["username"]), me["id"]

	rec := &contract.Recorder{Next: http.DefaultTransport, Scrub: scrubber(t, a, clone)}
	r := newRunner(t, repo.String(), identity, "https://"+repo.Host, rec, token)
	var steps []step
	poll := func(what string) {
		time.Sleep(4 * time.Second) // let GitLab's lists catch up
		rec.Name = what
		w := r.poll()
		t.Logf("%s:\n%s", what, w)
		steps = append(steps, step{Name: what, Wake: w})
	}
	poll("first poll: what is there is no change")

	issue := a.do("POST", a.proj+"/issues", map[string]any{"title": "Spec: export the report", "description": "Export as CSV.",
		"labels": "type::spec", "assignee_ids": []any{myID}})
	n := num(issue, "iid")
	poll("a new spec in the partition")

	a.do("PUT", a.proj+"/issues/"+n, map[string]any{"add_labels": "ready-for-dev,in-progress"})
	a.do("POST", a.proj+"/issues/"+n+"/notes", map[string]any{"body": "Please also support XLSX."})
	a.do("POST", a.proj+"/issues/"+n+"/notes", map[string]any{"body": "Started on it.\n\n<kbd>🤖 unattendish agent</kbd>"})
	poll("ready label, agent label, a comment and an echo")

	a.do("PUT", a.proj+"/issues/"+n, map[string]any{"description": "Export as CSV.\n\nAnd XLSX."})
	poll("a description edit, held")
	poll("the edit holds still: reported")
	poll("nothing moved: every list answers 304")

	branch := fmt.Sprintf("ww-%d", time.Now().Unix())
	// A file of the run's own, so its first line is in the diff whatever an
	// earlier run merged.
	file := branch + ".txt"
	sh(t, clone, "", "git", "checkout", "-q", "-B", branch, "origin/main")
	if err := os.WriteFile(filepath.Join(clone, file), []byte("csv\n"), 0o600); err != nil { //nolint:gosec // the recorder writes its own testdata and the sandbox clone it was told
		t.Fatal(err)
	}
	sh(t, clone, "", "git", "add", file)
	sh(t, clone, "", "git", "commit", "-qm", "feat: export as CSV")
	sh(t, clone, "", "git", "push", "-q", "origin", branch)
	mr := a.do("POST", a.proj+"/merge_requests", map[string]any{"source_branch": branch, "target_branch": "main",
		"title": "Export as CSV", "description": "Closes #" + n})
	m := num(mr, "iid")
	poll("a new merge request of the identity's")

	head := sh(t, clone, "", "git", "rev-parse", "HEAD")
	var refs map[string]any
	for i := 0; i < 20 && refs == nil; i++ { // GitLab computes the diff after the merge request exists
		time.Sleep(time.Second)
		refs, _ = a.do("GET", a.proj+"/merge_requests/"+m, nil)["diff_refs"].(map[string]any)
	}
	if refs == nil {
		t.Fatal("the merge request has no diff yet")
	}
	a.do("POST", a.proj+"/merge_requests/"+m+"/notes", map[string]any{"body": "Looks good so far."})
	a.do("POST", a.proj+"/merge_requests/"+m+"/discussions", map[string]any{"body": "Name this file export.csv.",
		"position": map[string]any{"position_type": "text", "base_sha": refs["base_sha"], "start_sha": refs["start_sha"], "head_sha": head,
			"new_path": file, "old_path": file, "new_line": 1}})
	a.do("POST", a.proj+"/merge_requests/"+m+"/approve", nil)
	poll("a comment, an inline comment and an approval")

	if err := os.WriteFile(filepath.Join(clone, file), []byte("csv\nxlsx\n"), 0o600); err != nil { //nolint:gosec // the recorder writes its own testdata and the sandbox clone it was told
		t.Fatal(err)
	}
	sh(t, clone, "", "git", "commit", "-qam", "feat: export as XLSX", "--author", "Ada Reviewer <ada@example.com>")
	sh(t, clone, "", "git", "push", "-q", "origin", branch)
	poll("a push with someone else's commit")

	a.do("PUT", a.proj+"/merge_requests/"+m+"/merge", map[string]any{"should_remove_source_branch": true})
	poll("merged, which closes the spec it names")
	poll("nothing moved: the lists answer 304 again")
	sh(t, clone, "", "git", "checkout", "-q", "main")
	sh(t, clone, "", "git", "pull", "-q", "origin", "main")

	c := rec.Scrub.Contract(rec.Contract("GitLab", "Recorded by record_test.go against a private sandbox project on "+time.Now().UTC().Format("2006-01-02")))
	if err := c.Save(contractFile); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(steps, "", "  ")
	if err := os.WriteFile(expectFile, []byte(rec.Scrub.Text(string(b))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
