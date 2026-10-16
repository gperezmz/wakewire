package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

var rules = domain.Rules{
	Identity: "acc-1",
	Labels:   domain.Labels{domain.MeaningSpec: "type::spec", domain.MeaningReady: "ready-for-dev", domain.MeaningInProgress: "in-progress", domain.MeaningNeedsInput: "needs-input"},
	Marker:   domain.DefaultMarker,
}

const spec = `{"id":"10001","key":"KAN-1","fields":{"summary":"Export","labels":["type::spec"],"updated":"2026-10-16T10:00:00.000+0000",
	"assignee":{"accountId":"acc-1","displayName":"Ada"},"reporter":{"accountId":"acc-1"},
	"status":{"id":"1","name":"To Do","statusCategory":{"key":"new"}},"comment":{"total":0,"comments":[]}}}`

const specLabelled = `{"id":"10001","key":"KAN-1","fields":{"summary":"Export","labels":["type::spec","ready-for-dev"],"updated":"2026-10-16T10:05:00.000+0000",
	"assignee":{"accountId":"acc-1","displayName":"Ada"},"reporter":{"accountId":"acc-1"},
	"status":{"id":"1","name":"To Do","statusCategory":{"key":"new"}},"comment":{"total":1,"comments":[]}}}`

// fakeJira answers by route and records each request.
type fakeJira struct {
	mu       sync.Mutex
	t        *testing.T
	routes   map[string]func(body map[string]any) (int, string)
	requests []string
	bodies   []map[string]any
	header   http.Header
}

func (f *fakeJira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	defer f.mu.Unlock()
	route := r.Method + " " + r.URL.Path
	f.requests = append(f.requests, route)
	f.bodies = append(f.bodies, body)
	if user, pass, ok := r.BasicAuth(); !ok || user != "me@example.com" || pass != "s3cr3t-value" {
		f.t.Errorf("%s: basic auth was %q", route, user)
	}
	h, ok := f.routes[route]
	if !ok {
		f.t.Errorf("unexpected %s", route)
		http.Error(w, "unexpected", 500)
		return
	}
	maps.Copy(w.Header(), f.header)
	status, resp := h(body)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(resp))
}

func newSource(t *testing.T, routes map[string]func(map[string]any) (int, string)) (*Source, *fakeJira) {
	f := &fakeJira{t: t, routes: routes, header: http.Header{"Date": {"Fri, 16 Oct 2026 10:10:00 GMT"}}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Source{HTTP: httpread.New("test", nil), Base: srv.URL, Site: "x.atlassian.net", Project: "KAN",
		Email: "me@example.com", Token: "s3cr3t-value", Name: "jira"}, f
}

func answer(s string) func(map[string]any) (int, string) {
	return func(map[string]any) (int, string) { return 200, s }
}

func page(issues ...string) string {
	return `{"issues":[` + strings.Join(issues, ",") + `],"isLast":true}`
}

func TestFirstPollReadsOnlyTheSearch(t *testing.T) {
	src, f := newSource(t, map[string]func(map[string]any) (int, string){"POST /rest/api/3/search/jql": answer(page(spec))})
	out, err := src.Poll(context.Background(), application.PollInput{Rules: rules, FirstPoll: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 || len(out.Observations) != 1 || len(out.Observations[0].Activities) != 0 {
		t.Fatalf("requests %v, observations %+v", f.requests, out.Observations)
	}
	o := out.Observations[0]
	if o.Item.Ref != "KAN-1" || o.Item.URL != "https://x.atlassian.net/browse/KAN-1" || o.At.IsZero() || !out.At.Equal(o.At) {
		t.Fatalf("%+v", o)
	}
	jql := f.bodies[0]["jql"].(string)
	for _, want := range []string{`project = "KAN"`, `labels = "type::spec"`, `assignee = "acc-1"`, `reporter = "acc-1" AND assignee is EMPTY`} {
		if !strings.Contains(jql, want) {
			t.Errorf("jql %q lacks %q", jql, want)
		}
	}
	if strings.Contains(jql, "issuekey") || f.bodies[0]["reconcileIssues"] != nil {
		t.Errorf("nothing is watched yet: %v", f.bodies[0])
	}
	if !strings.Contains(string(out.State), `"10001"`) {
		t.Errorf("the issue id is kept for reconcileIssues: %s", out.State)
	}
}

func TestMovedIssueReadsChangelogAndComments(t *testing.T) {
	src, f := newSource(t, map[string]func(map[string]any) (int, string){
		"POST /rest/api/3/search/jql": answer(page(specLabelled)),
		"POST /rest/api/3/changelog/bulkfetch": answer(`{"issueChangeLogs":[{"issueId":"10001","changeHistories":[
			{"id":"500","author":{"accountId":"acc-9","displayName":"Ada Reviewer"},"created":"2026-10-16T10:04:00.000+0000",
			 "items":[{"field":"labels","fieldId":"labels","fromString":"type::spec","toString":"type::spec ready-for-dev"}]}]}]}`),
		"GET /rest/api/3/issue/KAN-1/comment": answer(`{"total":1,"comments":[{"id":"900","author":{"displayName":"Ada Reviewer"},
			"created":"2026-10-16T10:05:00.000+0000","body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Please also XLSX."}]}]}}]}`),
	})
	snap := domain.Snapshot{}
	var i issue
	if err := json.Unmarshal([]byte(spec), &i); err != nil {
		t.Fatal(err)
	}
	snap.Item = i.item("x.atlassian.net")
	state, _ := json.Marshal(state{IDs: map[string]string{"KAN-1": "10001"}, Statuses: map[string]string{}})
	out, err := src.Poll(context.Background(), application.PollInput{Rules: rules, Snapshots: map[string]domain.Snapshot{"KAN-1": snap}, State: state})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Observations) != 1 {
		t.Fatalf("%+v", out.Observations)
	}
	var got []string
	for _, a := range out.Observations[0].Activities {
		got = append(got, a.ID+" "+string(a.Kind)+" "+a.By+" "+a.Body)
	}
	want := "cl:500:0 label_added Ada Reviewer ;c:900 comment Ada Reviewer Please also XLSX."
	if strings.Join(got, ";") != want {
		t.Fatalf("got %q", got)
	}
	jql := f.bodies[0]["jql"].(string)
	if !strings.Contains(jql, `issuekey in ("KAN-1")`) {
		t.Errorf("a watched issue stays in view: %s", jql)
	}
	if ids, _ := f.bodies[0]["reconcileIssues"].([]any); len(ids) != 1 || ids[0].(float64) != 10001 {
		t.Errorf("reconcileIssues: %v", f.bodies[0]["reconcileIssues"])
	}
}

func TestBulkChangelogFailureFallsBackToOneRequestEach(t *testing.T) {
	src, f := newSource(t, map[string]func(map[string]any) (int, string){
		"POST /rest/api/3/search/jql":          answer(page(specLabelled)),
		"POST /rest/api/3/changelog/bulkfetch": func(map[string]any) (int, string) { return 500, `{}` },
		"GET /rest/api/3/issue/KAN-1/changelog": answer(`{"isLast":true,"total":1,"values":[{"id":"500","created":"2026-10-16T10:04:00.000+0000",
			"items":[{"field":"labels","fromString":"","toString":"ready-for-dev"}]}]}`),
		"GET /rest/api/3/issue/KAN-1/comment": answer(`{"total":1,"comments":[]}`),
	})
	var i issue
	_ = json.Unmarshal([]byte(spec), &i)
	snap := domain.Snapshot{Item: i.item("x.atlassian.net")}
	out, err := src.Poll(context.Background(), application.PollInput{Rules: rules, Snapshots: map[string]domain.Snapshot{"KAN-1": snap}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Observations[0].Activities) != 1 || out.Observations[0].Activities[0].Label != "ready-for-dev" {
		t.Fatalf("%+v (requests %v)", out.Observations[0].Activities, f.requests)
	}
}

func TestSameFieldsAreNoObservationAndAGoneIssueIsReportedOnce(t *testing.T) {
	src, f := newSource(t, map[string]func(map[string]any) (int, string){"POST /rest/api/3/search/jql": answer(page(spec))})
	var i issue
	_ = json.Unmarshal([]byte(spec), &i)
	snaps := map[string]domain.Snapshot{"KAN-1": {Item: i.item("x.atlassian.net")}, "KAN-2": {Item: domain.Item{Ref: "KAN-2"}}}
	out, err := src.Poll(context.Background(), application.PollInput{Rules: rules, Snapshots: snaps})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Observations) != 1 || out.Observations[0].Item.Ref != "KAN-2" || !out.Observations[0].OffList {
		t.Fatalf("%+v", out.Observations)
	}
	snaps["KAN-2"] = domain.Snapshot{Item: domain.Item{Ref: "KAN-2"}, OffList: true}
	if out, _ = src.Poll(context.Background(), application.PollInput{Rules: rules, Snapshots: snaps}); len(out.Observations) != 0 {
		t.Fatalf("asked about again: %+v", out.Observations)
	}
	// Jira may answer 400 for a deleted key: an off-list issue is left out of the query.
	if jql := f.bodies[len(f.bodies)-1]["jql"].(string); strings.Contains(jql, "KAN-2") || !strings.Contains(jql, "KAN-1") {
		t.Errorf("the query asks about an off-list issue: %s", jql)
	}
	if ids, _ := f.bodies[len(f.bodies)-1]["reconcileIssues"].([]any); len(ids) != 0 {
		t.Errorf("reconcileIssues: %v", ids)
	}
}

func TestSearchPagesFollowTheToken(t *testing.T) {
	calls := 0
	src, _ := newSource(t, map[string]func(map[string]any) (int, string){"POST /rest/api/3/search/jql": func(b map[string]any) (int, string) {
		calls++
		if b["nextPageToken"] == nil {
			return 200, `{"issues":[` + spec + `],"nextPageToken":"p2","isLast":false}`
		}
		return 200, `{"issues":[` + strings.Replace(spec, "KAN-1", "KAN-2", 1) + `],"isLast":true}`
	}})
	out, err := src.Poll(context.Background(), application.PollInput{Rules: rules, FirstPoll: true})
	if err != nil || calls != 2 || len(out.Observations) != 2 {
		t.Fatalf("calls %d, err %v, %+v", calls, err, out.Observations)
	}
}

// A busy issue has more comments or changes than the pages the source reads:
// the ones read must be the newest, or what is new is never fetched.
func TestABusyIssueIsReadFromItsEnd(t *testing.T) {
	const total = 250
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		asked = append(asked, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]+"@"+strconv.Itoa(start))
		var items []string
		for i := start; i < min(start+100, total); i++ {
			if strings.HasSuffix(r.URL.Path, "/comment") {
				items = append(items, fmt.Sprintf(`{"id":"%d","created":"2026-10-16T10:00:00.000+0000"}`, i))
			} else {
				items = append(items, fmt.Sprintf(`{"id":"%d","created":"2026-10-16T10:00:00.000+0000","items":[{"field":"labels","toString":"l%d"}]}`, i, i))
			}
		}
		key := map[bool]string{true: "comments", false: "values"}[strings.HasSuffix(r.URL.Path, "/comment")]
		_, _ = fmt.Fprintf(w, `{"%s":[%s],"total":%d}`, key, strings.Join(items, ","), total)
	}))
	defer srv.Close()
	src := &Source{HTTP: httpread.New("test", nil), Base: srv.URL, Site: "x.atlassian.net", Project: "KAN",
		Email: "me@example.com", Token: "s3cr3t-value", Name: "jira", Pages: 1}
	c := &client{http: src.HTTP, base: srv.URL, email: src.Email, token: src.Token}

	comments, err := src.comments(context.Background(), c, "KAN-1")
	if err != nil || len(comments) != 100 || comments[0].ID != "150" || comments[99].ID != "249" {
		t.Fatalf("%d comments from %v: %v", len(comments), asked, err)
	}
	hs, err := src.issueChangelog(context.Background(), c, "KAN-1")
	if err != nil || len(hs) != 100 || hs[0].ID != "150" || hs[99].ID != "249" {
		t.Fatalf("%d histories from %v: %v", len(hs), asked, err)
	}
	if want := "comment@0 comment@150 changelog@0 changelog@150"; strings.Join(asked, " ") != want {
		t.Fatalf("asked %v, want %s", asked, want)
	}
}

// The bulk changelog pages by token with no way to ask for the end, so one
// longer than the cap is read issue by issue instead.
func TestABulkChangelogLongerThanTheCapFallsBackToTheIssuesOwn(t *testing.T) {
	bulk := 0
	src, f := newSource(t, map[string]func(map[string]any) (int, string){
		"POST /rest/api/3/search/jql": answer(page(specLabelled)),
		"POST /rest/api/3/changelog/bulkfetch": func(map[string]any) (int, string) {
			bulk++
			return 200, `{"issueChangeLogs":[],"nextPageToken":"more"}`
		},
		"GET /rest/api/3/issue/KAN-1/changelog": answer(`{"isLast":true,"total":1,"values":[{"id":"500","created":"2026-10-16T10:04:00.000+0000",
			"items":[{"field":"labels","fromString":"","toString":"ready-for-dev"}]}]}`),
		"GET /rest/api/3/issue/KAN-1/comment": answer(`{"total":1,"comments":[]}`),
	})
	src.Pages = 2
	var i issue
	_ = json.Unmarshal([]byte(spec), &i)
	snap := domain.Snapshot{Item: i.item("x.atlassian.net")}
	out, err := src.Poll(context.Background(), application.PollInput{Rules: rules, Snapshots: map[string]domain.Snapshot{"KAN-1": snap}})
	if err != nil || bulk != 2 || len(out.Observations[0].Activities) != 1 {
		t.Fatalf("bulk %d, err %v, %+v (requests %v)", bulk, err, out.Observations, f.requests)
	}
}

func TestNearLimitAndRateLimitAreRead(t *testing.T) {
	src, f := newSource(t, map[string]func(map[string]any) (int, string){"POST /rest/api/3/search/jql": answer(page())})
	f.header.Set("X-RateLimit-NearLimit", "true")
	out, err := src.Poll(context.Background(), application.PollInput{Rules: rules, FirstPoll: true})
	if err != nil || !out.Rate.NearLimit {
		t.Fatalf("%v %+v", err, out.Rate)
	}
	f.header = http.Header{"Retry-After": {"30"}}
	f.routes["POST /rest/api/3/search/jql"] = func(map[string]any) (int, string) { return 429, `{}` }
	_, err = src.Poll(context.Background(), application.PollInput{Rules: rules, FirstPoll: true})
	var coded interface{ Code() string }
	if !errors.As(err, &coded) || coded.Code() != "rate_limited" {
		t.Fatalf("%v", err)
	}
}

func TestRefusedTokenNamesNoToken(t *testing.T) {
	src, _ := newSource(t, map[string]func(map[string]any) (int, string){"POST /rest/api/3/search/jql": func(map[string]any) (int, string) { return 401, `{}` }})
	_, err := src.Poll(context.Background(), application.PollInput{Rules: rules, FirstPoll: true})
	var coded interface{ Code() string }
	if !errors.As(err, &coded) || coded.Code() != "token_refused" || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("%v", err)
	}
}

func TestJQLQuoting(t *testing.T) {
	if got := quote(`a"b\c`); got != `"a\"b\\c"` {
		t.Fatal(got)
	}
}
