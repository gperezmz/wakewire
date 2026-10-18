//go:build record

package jira_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/adapters/contract"
)

// Records the scenario against the real sandbox (the KAN project of
// example.atlassian.net), making real changes on it between polls with
// a plain HTTP client. The writes are not part of wakewire and are not
// recorded; only the source's own reads are. The credential is the account
// stored for the site (`wakewire account add`), read through the account
// service:
//
//	go test -tags record -run TestRecordScenario ./internal/watch/adapters/jira/
//
// It writes testdata/contract.json, scrubbed, and testdata/expect.json.

// scrubber replaces the sandbox's site, account and the names and email
// Jira and git hold for the person recording.
func scrubber(sb *sandbox) contract.Scrubber {
	replace := [][2]string{{realSiteTag(), "wwsite"}, {sb.accountID, account}, {url.QueryEscape(sb.accountID), account},
		{sb.email, "ww@example.com"}}
	names := []string{sb.displayName}
	if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		names = append(names, strings.TrimSpace(string(out)))
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		replace = append(replace, [2]string{n, person})
		for part := range strings.FieldsSeq(n) {
			if len(part) >= 4 {
				replace = append(replace, [2]string{part, "Wakewire"})
			}
		}
	}
	return contract.Scrubber{
		Replace:      replace,
		KeepResponse: []string{"Date", "X-RateLimit-NearLimit"},
		// Fields that name a person or a place the client never reads.
		Drop: []string{"avatarUrls", "timeZone", "locale"},
	}
}

// TestRescrub applies the scrubber again to a recording already written.
func TestRescrub(t *testing.T) {
	c, err := contract.Load(contractFile)
	if err != nil {
		t.Skip(err)
	}
	s := scrubber(newSandbox(t))
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

func adf(paragraphs ...string) map[string]any {
	var content []any
	for _, p := range paragraphs {
		content = append(content, map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": p}}})
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// create makes an issue of the sandbox's first ordinary issue type.
func (sb *sandbox) create(summary string, labels []string, assignee string, description map[string]any) string {
	sb.t.Helper()
	var project struct {
		IssueTypes []struct {
			Name    string `json:"name"`
			Subtask bool   `json:"subtask"`
		} `json:"issueTypes"`
	}
	sb.get("/rest/api/3/project/KAN", &project)
	typeName := ""
	for _, it := range project.IssueTypes {
		if it.Name == "Task" {
			typeName = it.Name
		}
		if typeName == "" && !it.Subtask {
			typeName = it.Name
		}
	}
	fields := map[string]any{"project": map[string]any{"key": "KAN"}, "summary": summary, "issuetype": map[string]any{"name": typeName},
		"labels": labels, "description": description}
	if assignee != "" {
		fields["assignee"] = map[string]any{"accountId": assignee}
	}
	var made struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(sb.send(http.MethodPost, "/rest/api/3/issue", map[string]any{"fields": fields}), &made); err != nil {
		sb.t.Fatal(err)
	}
	return made.Key
}

func (sb *sandbox) update(key string, body map[string]any) {
	sb.t.Helper()
	sb.send(http.MethodPut, "/rest/api/3/issue/"+key, body)
}

func (sb *sandbox) addLabels(key string, labels ...string) {
	var ops []any
	for _, l := range labels {
		ops = append(ops, map[string]any{"add": l})
	}
	sb.update(key, map[string]any{"update": map[string]any{"labels": ops}})
}

func (sb *sandbox) comment(key string, paragraphs ...string) {
	sb.t.Helper()
	sb.send(http.MethodPost, "/rest/api/3/issue/"+key+"/comment", map[string]any{"body": adf(paragraphs...)})
}

func (sb *sandbox) assign(key, accountID string) {
	sb.t.Helper()
	var who any
	if accountID != "" {
		who = accountID
	}
	sb.send(http.MethodPut, "/rest/api/3/issue/"+key+"/assignee", map[string]any{"accountId": who})
}

// done moves an issue through the transition that ends in a status of the
// done category.
func (sb *sandbox) done(key string) {
	sb.t.Helper()
	var ts struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				StatusCategory struct {
					Key string `json:"key"`
				} `json:"statusCategory"`
			} `json:"to"`
		} `json:"transitions"`
	}
	sb.get("/rest/api/3/issue/"+key+"/transitions", &ts)
	for _, tr := range ts.Transitions {
		if tr.To.StatusCategory.Key == "done" {
			sb.send(http.MethodPost, "/rest/api/3/issue/"+key+"/transitions", map[string]any{"transition": map[string]any{"id": tr.ID}})
			return
		}
	}
	sb.t.Fatalf("%s has no transition to Done", key)
}

// someoneElse answers another user the project's issues can be assigned to,
// or "" when the site has none.
func (sb *sandbox) someoneElse() string {
	var users []struct {
		AccountID   string `json:"accountId"`
		AccountType string `json:"accountType"`
		Active      bool   `json:"active"`
	}
	sb.get("/rest/api/3/user/assignable/search?project=KAN&maxResults=50", &users)
	for _, u := range users {
		if u.AccountID != sb.accountID && u.AccountType == "atlassian" && u.Active {
			return u.AccountID
		}
	}
	return ""
}

func TestRecordScenario(t *testing.T) {
	time.Local = time.UTC
	sb := newSandbox(t)
	rec := &contract.Recorder{Next: http.DefaultTransport, Scrub: scrubber(sb)}
	// Only the source's own transport is recorded; the writes above go through sb.
	r := newRunner(t, realSite(), sb.accountID, "", rec, sb.email, sb.token)
	var steps []step
	poll := func(what string) {
		time.Sleep(4 * time.Second)
		rec.Name = what
		w := r.poll()
		t.Logf("%s:\n%s", what, w)
		steps = append(steps, step{Name: what, Wake: w})
	}
	poll("first poll: what is there is no change")

	spec := sb.create("Spec: export the report", []string{"type::spec"}, sb.accountID, adf("Export as CSV."))
	t.Logf("created %s, indexed after %s", spec, sb.waitIndexed(spec))
	poll("a new spec in the partition")

	sb.addLabels(spec, "ready-for-dev", "in-progress")
	sb.comment(spec, "Please also support XLSX.")
	sb.comment(spec, "Started on it.", "🤖 unattendish agent")
	poll("ready label, agent label, a comment and an echo")

	sb.update(spec, map[string]any{"fields": map[string]any{"description": adf("Export as CSV.", "And XLSX.")}})
	poll("a description edit, held")
	poll("the edit holds still: reported")
	poll("nothing moved")

	second := sb.create("Spec: import the report", []string{"type::spec"}, sb.accountID, adf("Import from CSV."))
	t.Logf("created %s, indexed after %s", second, sb.waitIndexed(second))
	poll("a second spec")

	if other := sb.someoneElse(); other != "" {
		sb.assign(spec, other)
	} else {
		t.Log("the site has no other user: the spec is unassigned and loses its spec label instead")
		sb.assign(spec, "")
		sb.update(spec, map[string]any{"update": map[string]any{"labels": []any{map[string]any{"remove": "type::spec"}}}})
	}
	poll("the first spec leaves the partition")

	sb.done(second)
	poll("the second spec done")
	poll("nothing moved again")

	c := rec.Scrub.Contract(rec.Contract("Jira", "Recorded by record_test.go against the KAN project of "+realSite()+" on "+time.Now().UTC().Format("2006-01-02")))
	if err := c.Save(contractFile); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(steps, "", "  ")
	if err := os.WriteFile(expectFile, []byte(rec.Scrub.Text(string(b))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
