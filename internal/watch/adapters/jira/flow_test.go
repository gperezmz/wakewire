package jira_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A small Jira of hand-written state, to run the whole watch over the
// source without a recording: the wakes the source's answers lead to.
type fakeProject struct {
	assignee  string
	labels    []string
	status    string // the status id: 1 To Do, 3 Done
	desc      string
	comments  []string // ADF paragraphs, one comment each
	histories []string // changelog histories, as JSON
	updated   int
}

func (p *fakeProject) issue() string {
	cat, name := "new", "To Do"
	if p.status == "3" {
		cat, name = "done", "Done"
	}
	assignee := "null"
	if p.assignee != "" {
		assignee = `{"accountId":"` + p.assignee + `","displayName":"Ada"}`
	}
	labels, _ := json.Marshal(p.labels)
	desc, _ := json.Marshal(p.desc)
	return fmt.Sprintf(`{"id":"10001","key":"KAN-1","fields":{"summary":"Export","labels":%s,"updated":"2026-10-16T10:%02d:00.000+0000",
		"description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":%s}]}]},
		"assignee":%s,"reporter":{"accountId":"acc-me"},"status":{"id":%q,"name":%q,"statusCategory":{"key":%q}},
		"comment":{"total":%d,"comments":[]}}}`, labels, p.updated, desc, assignee, p.status, name, cat, len(p.comments))
}

func (p *fakeProject) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	switch r.Method + " " + r.URL.Path {
	case "POST /rest/api/3/search/jql":
		var req struct{ JQL string }
		_ = json.Unmarshal(body, &req)
		inSet := p.assignee == "acc-me" || p.assignee == ""
		if inSet || strings.Contains(req.JQL, "issuekey in") {
			_, _ = fmt.Fprintf(w, `{"issues":[%s],"isLast":true}`, p.issue())
			return
		}
		_, _ = w.Write([]byte(`{"issues":[],"isLast":true}`))
	case "POST /rest/api/3/changelog/bulkfetch":
		_, _ = fmt.Fprintf(w, `{"issueChangeLogs":[{"issueId":"10001","changeHistories":[%s]}]}`, strings.Join(p.histories, ","))
	case "GET /rest/api/3/issue/KAN-1/comment":
		var cs []string
		for i, c := range p.comments {
			cs = append(cs, fmt.Sprintf(`{"id":"%d","author":{"displayName":"Ada Reviewer"},"created":"2027-01-01T11:%02d:00.000+0000",
				"body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":%q}]}]}}`, 900+i, i, c))
		}
		_, _ = fmt.Fprintf(w, `{"total":%d,"comments":[%s]}`, len(cs), strings.Join(cs, ","))
	default:
		http.Error(w, "unexpected "+r.URL.Path, http.StatusInternalServerError)
	}
}

func TestWakesFromAHandWrittenJira(t *testing.T) {
	time.Local = time.UTC
	p := &fakeProject{assignee: "acc-me", labels: []string{"type::spec"}, status: "1", desc: "Export as CSV."}
	srv := httptest.NewServer(http.HandlerFunc(p.serve))
	defer srv.Close()
	r := newRunner(t, site, "acc-me", srv.URL, nil, "ww@example.com", "replayed")

	expect := func(what, want string) {
		t.Helper()
		got := r.poll()
		// Drop the header line's times and batch number: only the lines are judged.
		var lines []string
		for _, l := range strings.Split(got, "\n") {
			if strings.HasPrefix(l, "- ") {
				lines = append(lines, l)
			}
		}
		if strings.Join(lines, "\n") != want {
			t.Errorf("%s:\ngot  %q\nwant %q", what, strings.Join(lines, "\n"), want)
		}
	}
	expect("first poll", "")
	expect("idle", "")

	p.labels, p.updated = []string{"type::spec", "ready-for-dev", "in-progress"}, 1
	p.histories = append(p.histories, `{"id":"500","author":{"accountId":"acc-x","displayName":"Ada Reviewer"},"created":"2027-01-01T11:00:00.000+0000",
		"items":[{"field":"labels","fieldId":"labels","fromString":"type::spec","toString":"type::spec ready-for-dev in-progress"}]}`)
	p.comments = []string{"Please also support XLSX.", "Started.\n\n🤖 unattendish agent"}
	expect("labels and comments", "- KAN-1: label \"ready-for-dev\" added by Ada\n- KAN-1: comment by Ada")

	p.desc, p.updated = "Export as CSV. And XLSX.", 2
	p.histories = append(p.histories, `{"id":"501","author":{"accountId":"acc-x","displayName":"Ada Reviewer"},"created":"2027-01-01T12:00:00.000+0000",
		"items":[{"field":"description","fieldId":"description"}]}`)
	expect("an edit is held", "")
	expect("and reported when it holds", "- KAN-1: description edited by Ada")

	p.assignee, p.updated = "acc-other", 3
	p.histories = append(p.histories, `{"id":"502","author":{"accountId":"acc-me","displayName":"Ada Reviewer"},"created":"2027-01-01T13:00:00.000+0000",
		"items":[{"field":"assignee","fieldId":"assignee","from":"acc-me","to":"acc-other"}]}`)
	expect("reassigned away", "- KAN-1: reassigned by Ada")
	expect("gone from the set", "")
}
