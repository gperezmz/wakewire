package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// fake is a GitLab that answers canned bodies by path; a path with an ETag
// answers 304 to its own tag.
type fake struct {
	mu       sync.Mutex
	bodies   map[string]string
	status   map[string]int
	seen     []string // method and request URI of every request
	tokens   map[string]bool
	etagged  map[string]bool
	rateless bool
}

func newFake(bodies map[string]string) *fake {
	return &fake{bodies: bodies, status: map[string]int{}, tokens: map[string]bool{}, etagged: map[string]bool{}}
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.Method+" "+r.URL.RequestURI())
	f.tokens[r.Header.Get("PRIVATE-TOKEN")] = true
	w.Header().Set("Date", "Fri, 16 Oct 2026 20:00:00 GMT")
	if !f.rateless {
		w.Header().Set("RateLimit-Limit", "2000")
		w.Header().Set("RateLimit-Remaining", "1990")
		w.Header().Set("RateLimit-Reset", "1792180860")
	}
	body, ok := f.bodies[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if st := f.status[r.URL.Path]; st != 0 {
		w.WriteHeader(st)
		return
	}
	if f.etagged[r.URL.Path] {
		tag := `W/"` + r.URL.Path + body + `"`
		w.Header().Set("Etag", tag)
		if r.Header.Get("If-None-Match") == tag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	_, _ = w.Write([]byte(body))
}

func (f *fake) requests(sub string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.seen {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

const proj = "/api/v4/projects/grp/sub/proj"

func sourceOf(srv *httptest.Server) *Source {
	r, _ := domain.ParseRepo("gitlab.example.com/grp/sub/proj")
	return &Source{HTTP: httpread.New("wakewire", nil), Base: srv.URL, Token: "secret-token", Repo: r, Name: "work"}
}

func rules() domain.Rules {
	return domain.Rules{Identity: "ada", Labels: domain.Labels{domain.MeaningSpec: "type::spec", domain.MeaningReady: "ready-for-dev"}, Marker: domain.DefaultMarker}
}

func baseBodies() map[string]string {
	return map[string]string{
		"/api/v4/users": `[{"id":7,"username":"ada"}]`,
		proj + "/issues": `[
			{"iid":1,"title":"Spec","description":"Export.","state":"opened","labels":["type::spec","ready-for-dev"],
			 "assignees":[{"id":7,"username":"ada"}],"author":{"id":8,"username":"bob"},"user_notes_count":1,
			 "updated_at":"2026-10-16T19:00:00.000Z","web_url":"https://gitlab.example.com/grp/sub/proj/-/issues/1"}]`,
		proj + "/merge_requests": `[
			{"iid":4,"title":"Export","description":"Closes #1","state":"opened","labels":[],"assignees":[],
			 "author":{"id":7,"username":"ada"},"user_notes_count":0,"sha":"aaa111","merged_at":null,
			 "updated_at":"2026-10-16T19:01:00.000Z","web_url":"https://gitlab.example.com/grp/sub/proj/-/merge_requests/4"}]`,
		// The merge request alone is current where the list is cached.
		proj + "/merge_requests/4": `{"iid":4,"title":"Export","description":"Closes #1","state":"opened","labels":[],"assignees":[],
			 "author":{"id":7,"username":"ada"},"user_notes_count":2,"sha":"bbb222","merged_at":null,
			 "updated_at":"2026-10-16T19:01:00.000Z","web_url":"https://gitlab.example.com/grp/sub/proj/-/merge_requests/4"}`,
		proj + "/merge_requests/4/approvals": `{"approved_by":[]}`,
	}
}

func poll1(t *testing.T, s *Source, in application.PollInput) application.PollOutput {
	t.Helper()
	if in.Rules.Identity == "" {
		in.Rules = rules()
	}
	in.IncludeIssues = true
	out, err := s.Poll(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func byRef(out application.PollOutput) map[string]domain.Observation {
	m := map[string]domain.Observation{}
	for _, o := range out.Observations {
		m[o.Item.Ref] = o
	}
	return m
}

func TestFirstPollListsItemsAndReadsNoTimeline(t *testing.T) {
	f := newFake(baseBodies())
	srv := httptest.NewServer(f)
	defer srv.Close()
	out := poll1(t, sourceOf(srv), application.PollInput{FirstPoll: true})
	got := byRef(out)

	issue := got["#1"].Item
	if issue.State != "open" || issue.Author != "bob" || !slices.Equal(issue.Assignees, []string{"ada"}) ||
		!slices.Equal(issue.Labels, []string{"ready-for-dev", "type::spec"}) || issue.Body != "Export." || issue.Comments != 1 {
		t.Errorf("issue: %+v", issue)
	}
	mr := got["!4"].Item
	if mr.Kind != domain.ItemPull || mr.Head != "bbb222" || mr.Comments != 2 || mr.MergedAt != "" {
		t.Errorf("the merge request holds the list's stale head or count instead of its own: %+v", mr)
	}
	if n := f.requests("resource_"); n != 0 {
		t.Errorf("the first poll read %d timelines", n)
	}
	if !f.tokens["secret-token"] || len(f.tokens) != 1 {
		t.Errorf("tokens sent: %v", f.tokens)
	}
	if out.Rate.Limit != 2000 || out.Rate.Remaining != 1990 || out.Rate.ResetAt.Unix() != 1792180860 {
		t.Errorf("rate: %+v", out.Rate)
	}
	if !out.At.Equal(time.Date(2026, 10, 16, 20, 0, 0, 0, time.UTC)) {
		t.Errorf("service time: %v", out.At)
	}
	// The project's path is one escaped segment, and the three lists filter on the user's id.
	for _, want := range []string{
		"GET /api/v4/users?username=ada",
		"assignee_id=7", "author_id=7&", "assignee_id=None",
	} {
		if f.requests(want) == 0 {
			t.Errorf("no request with %q in %v", want, f.seen)
		}
	}
	var st state
	if err := json.Unmarshal(out.State, &st); err != nil || st.Users["ada"] != 7 || len(st.Lists) != 3 {
		t.Errorf("state: %s (%v)", out.State, err)
	}
}

func TestAnIdlePollKeepsTheETagsAndTheUserId(t *testing.T) {
	f := newFake(baseBodies())
	f.etagged[proj+"/issues"], f.etagged[proj+"/merge_requests"] = true, true
	f.rateless = true
	srv := httptest.NewServer(f)
	defer srv.Close()
	s := sourceOf(srv)
	first := poll1(t, s, application.PollInput{FirstPoll: true})
	snaps := map[string]domain.Snapshot{}
	for _, o := range first.Observations {
		snaps[o.Item.Ref] = domain.Snapshot{Item: o.Item}
	}
	second := poll1(t, s, application.PollInput{State: first.State, Snapshots: snaps})
	if len(second.Observations) != 0 {
		t.Errorf("an idle poll observed %+v", second.Observations)
	}
	if n := f.requests("/users"); n != 1 {
		t.Errorf("the user was resolved %d times", n)
	}
	if second.Rate != (application.RateInfo{}) {
		t.Errorf("an instance without limits gave a rate: %+v", second.Rate)
	}
}

func TestAMovedItemIsReadForItsActivity(t *testing.T) {
	b := baseBodies()
	b[proj+"/issues/1/resource_label_events"] = `[
		{"id":11,"user":{"username":"bob"},"created_at":"2026-10-16T19:10:00.000Z","action":"add","label":{"name":"ready-for-dev"}},
		{"id":12,"user":{"username":"bob"},"created_at":"2026-10-16T19:11:00.000Z","action":"remove","label":{"name":"in-progress"}},
		{"id":13,"user":{"username":"bob"},"created_at":"2026-10-16T19:11:00.000Z","action":"add","label":null}]`
	b[proj+"/issues/1/resource_state_events"] = `[{"id":21,"user":{"username":"bob"},"created_at":"2026-10-16T19:12:00.000Z","state":"closed"}]`
	b[proj+"/issues/1/notes"] = `[
		{"id":31,"type":null,"body":"Please add XLSX.","author":{"username":"bob"},"created_at":"2026-10-16T19:13:00.000Z","system":false},
		{"id":32,"type":null,"body":"changed the description","author":{"username":"bob"},"created_at":"2026-10-16T19:14:00.000Z","system":true},
		{"id":33,"type":null,"body":"mentioned in merge request !4","author":{"username":"bob"},"created_at":"2026-10-16T19:15:00.000Z","system":true}]`
	f := newFake(b)
	srv := httptest.NewServer(f)
	defer srv.Close()
	first := poll1(t, sourceOf(srv), application.PollInput{FirstPoll: true})
	snaps := map[string]domain.Snapshot{}
	for _, o := range first.Observations {
		snaps[o.Item.Ref] = domain.Snapshot{Item: o.Item}
	}
	moved := snaps["#1"]
	moved.Item.Comments = 0 // the issue gained a comment since the snapshot
	snaps["#1"] = moved

	out := poll1(t, sourceOf(srv), application.PollInput{State: first.State, Snapshots: snaps})
	got := byRef(out)
	if len(got) != 1 {
		t.Fatalf("observed %v", got)
	}
	var ids []string
	for _, a := range got["#1"].Activities {
		ids = append(ids, a.ID+" "+string(a.Kind))
	}
	want := []string{"le:11 label_added", "le:12 label_removed", "se:21 closed", "n:31 comment", "n:32 edited"}
	if !slices.Equal(ids, want) {
		t.Errorf("activities %v, want %v", ids, want)
	}
	c := got["#1"].Activities[3]
	if c.By != "bob" || c.Body != "Please add XLSX." || c.URL != "https://gitlab.example.com/grp/sub/proj/-/issues/1#note_31" {
		t.Errorf("comment: %+v", c)
	}
	if a := got["#1"].Activities[4]; a.Field != "description" || a.By != "bob" {
		t.Errorf("edit: %+v", a)
	}
}

func TestAMergeRequestWithAnApprovalAMergeAndNewCommits(t *testing.T) {
	b := baseBodies()
	b[proj+"/merge_requests/4"] = strings.Replace(b[proj+"/merge_requests/4"], `"state":"opened"`, `"state":"merged"`, 1)
	b[proj+"/merge_requests/4"] = strings.Replace(b[proj+"/merge_requests/4"], `"merged_at":null`, `"merged_at":"2026-10-16T19:30:00.000Z"`, 1)
	b[proj+"/merge_requests"] = strings.Replace(b[proj+"/merge_requests"], `"state":"opened"`, `"state":"merged"`, 1)
	b[proj+"/merge_requests/4/approvals"] = `{"approved_by":[{"user":{"username":"bob"}}]}`
	b[proj+"/merge_requests/4/resource_label_events"] = `[]`
	b[proj+"/merge_requests/4/resource_state_events"] = `[{"id":5,"user":{"username":"bob"},"created_at":"2026-10-16T19:30:00.000Z","state":"merged"}]`
	b[proj+"/merge_requests/4/notes"] = `[
		{"id":42,"type":"DiffNote","body":"Rename this.","author":{"username":"bob"},"created_at":"2026-10-16T19:20:00.000Z","system":false,
		 "position":{"new_path":"report.txt","old_path":"report.txt","new_line":1,"old_line":null}},
		{"id":41,"type":null,"body":"approved this merge request","author":{"username":"bob"},"created_at":"2026-10-16T19:21:00.000Z","system":true},
		{"id":40,"type":"DiscussionNote","body":"A thread reply.","author":{"username":"bob"},"created_at":"2026-10-16T19:19:00.000Z","system":false}]`
	b["/api/v4/user"] = `{"username":"ada","email":"ada@work.example","commit_email":"ada@example.com"}`
	// The commits come newest first: bbb222 is the head, aaa111 the one last seen.
	b[proj+"/merge_requests/4/commits"] = `[
		{"id":"bbb222","author_email":"ada@example.com"},
		{"id":"ccc333","author_email":"grace@example.com"},
		{"id":"aaa111","author_email":"ada@example.com"}]`
	f := newFake(b)
	srv := httptest.NewServer(f)
	defer srv.Close()
	snap := baseBodies()
	var e entry
	_ = json.Unmarshal([]byte(strings.Trim(snap[proj+"/merge_requests"], "[]")), &e)
	was := e.item(domain.ItemPull)
	was.Head = "aaa111"

	out := poll1(t, sourceOf(srv), application.PollInput{Snapshots: map[string]domain.Snapshot{"!4": {Item: was}}})
	o := byRef(out)["!4"]
	if o.Item.State != "closed" || o.Item.MergedAt == "" || o.Item.Head != "bbb222" {
		t.Fatalf("item: %+v", o.Item)
	}
	var ids []string
	for _, a := range o.Activities {
		ids = append(ids, a.ID+" "+string(a.Kind))
	}
	want := []string{"se:5 merged", "n:42 inline_comment", "n:40 comment", "ap:bob review", "commit:ccc333 pushed", "commit:bbb222 pushed"}
	if !slices.Equal(ids, want) {
		t.Fatalf("activities %v, want %v", ids, want)
	}
	if a := o.Activities[1]; a.Path != "report.txt" || a.Line != 1 || a.Hunk != "" || a.Body != "Rename this." {
		t.Errorf("inline comment: %+v", a)
	}
	if a := o.Activities[3]; a.State != "approved" || a.By != "bob" || !a.At.Equal(time.Date(2026, 10, 16, 19, 21, 0, 0, time.UTC)) {
		t.Errorf("approval: %+v", a)
	}
	if a, b := o.Activities[4].Commit.Author, o.Activities[5].Commit.Author; a != "grace@example.com" || b != "ada" {
		t.Errorf("commit authors %q, %q: the identity's email should read as its username", a, b)
	}
}

func TestAnApprovalAloneMakesAMergeRequestMove(t *testing.T) {
	b := baseBodies()
	b[proj+"/merge_requests/4/approvals"] = `{"approved_by":[{"user":{"username":"bob"},"approved_at":"2026-10-16T19:40:00.000Z"}]}`
	b[proj+"/merge_requests/4/resource_label_events"] = `[]`
	b[proj+"/merge_requests/4/resource_state_events"] = `[]`
	b[proj+"/merge_requests/4/notes"] = `[]`
	f := newFake(b)
	srv := httptest.NewServer(f)
	defer srv.Close()
	first := poll1(t, sourceOf(srv), application.PollInput{FirstPoll: true})
	snaps := map[string]domain.Snapshot{}
	for _, o := range first.Observations {
		snaps[o.Item.Ref] = domain.Snapshot{Item: o.Item}
	}
	out := poll1(t, sourceOf(srv), application.PollInput{State: first.State, Snapshots: snaps})
	got := byRef(out)
	if len(got) != 1 || len(got["!4"].Activities) != 1 || got["!4"].Activities[0].ID != "ap:bob" ||
		!got["!4"].Activities[0].At.Equal(time.Date(2026, 10, 16, 19, 40, 0, 0, time.UTC)) {
		t.Fatalf("observed %+v", out.Observations)
	}
	// Once recorded, it moves nothing.
	snap := snaps["!4"]
	snap.Seen = []string{"ap:bob"}
	snaps["!4"] = snap
	if out := poll1(t, sourceOf(srv), application.PollInput{State: first.State, Snapshots: snaps}); len(out.Observations) != 0 {
		t.Errorf("a recorded approval moved the item: %+v", out.Observations)
	}
}

func TestWithoutTheTrackersIssuesOnlyMergeRequestsAreListed(t *testing.T) {
	f := newFake(baseBodies())
	srv := httptest.NewServer(f)
	defer srv.Close()
	out, err := sourceOf(srv).Poll(context.Background(), application.PollInput{Rules: rules(), FirstPoll: true})
	if err != nil {
		t.Fatal(err)
	}
	if f.requests("/issues") != 0 || len(out.Observations) != 1 || out.Observations[0].Item.Ref != "!4" {
		t.Errorf("requests %v, observed %+v", f.seen, out.Observations)
	}
}

func TestAWatchedItemMissingFromTheListsIsReadAloneOnce(t *testing.T) {
	b := baseBodies()
	b[proj+"/issues"] = `[]`
	b[proj+"/issues/1"] = `{"iid":1,"title":"Spec","description":"Export.","state":"closed","labels":["type::spec"],
		"assignees":[{"username":"ada"}],"author":{"username":"bob"},"user_notes_count":1,"updated_at":"2026-10-16T19:50:00.000Z"}`
	b[proj+"/issues/1/resource_label_events"] = `[]`
	b[proj+"/issues/1/resource_state_events"] = `[]`
	b[proj+"/issues/1/notes"] = `[]`
	f := newFake(b)
	srv := httptest.NewServer(f)
	defer srv.Close()
	was := domain.Item{Ref: "#1", Kind: domain.ItemIssue, State: "open", Author: "bob", Title: "Spec", Body: "Export.", Comments: 1,
		Labels: []string{"type::spec"}, Assignees: []string{"ada"}}
	out := poll1(t, sourceOf(srv), application.PollInput{Snapshots: map[string]domain.Snapshot{"#1": {Item: was}}})
	o := byRef(out)["#1"]
	if !o.OffList || o.Item.State != "closed" {
		t.Errorf("observation: %+v", o)
	}
	// Told it is off the lists, the source does not read it alone again.
	was2 := o.Item
	out = poll1(t, sourceOf(srv), application.PollInput{State: out.State, Snapshots: map[string]domain.Snapshot{"#1": {Item: was2, OffList: true}}})
	if _, again := byRef(out)["#1"]; again {
		t.Errorf("read again: %+v", out.Observations)
	}
}

func TestErrorsCarryTheServiceCodesAndNeverTheToken(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		path   string
		code   string
	}{
		{"a refused token", 401, "/api/v4/users", "token_refused"},
		{"a forbidden token", 403, proj + "/merge_requests", "token_refused"},
		{"a project the token cannot see", 404, proj + "/merge_requests", "token_refused"},
		{"a server error", 502, proj + "/merge_requests", "unreachable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := baseBodies()
			f := newFake(b)
			f.status[c.path] = c.status
			srv := httptest.NewServer(f)
			defer srv.Close()
			_, err := sourceOf(srv).Poll(context.Background(), application.PollInput{Rules: rules(), FirstPoll: true})
			var he interface{ Code() string }
			if err == nil || !asCoded(err, &he) || he.Code() != c.code {
				t.Fatalf("error %v, want code %s", err, c.code)
			}
			if strings.Contains(err.Error(), "secret-token") {
				t.Errorf("the error holds the token: %v", err)
			}
		})
	}
}

func TestAnUnknownUserIsNamed(t *testing.T) {
	b := baseBodies()
	b["/api/v4/users"] = `[]`
	srv := httptest.NewServer(newFake(b))
	defer srv.Close()
	_, err := sourceOf(srv).Poll(context.Background(), application.PollInput{Rules: rules(), FirstPoll: true})
	if err == nil || !strings.Contains(err.Error(), `"ada"`) {
		t.Errorf("error: %v", err)
	}
}

func asCoded(err error, out *interface{ Code() string }) bool {
	c, ok := err.(interface{ Code() string }) //nolint:errorlint // httpread errors are returned as they are
	if ok {
		*out = c
	}
	return ok
}
