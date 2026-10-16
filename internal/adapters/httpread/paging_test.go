package httpread

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// pager serves total pages of two numbers each at /items?page=N, with a
// Link header, and records the requests it saw.
type pager struct {
	total    int
	requests []string
	srv      *httptest.Server
}

func newPager(t *testing.T, total int) *pager {
	p := &pager{total: total}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests = append(p.requests, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		n, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if n == 0 {
			n = 1
		}
		link := func(rel string, page int) string {
			return fmt.Sprintf(`<%s%s?page=%d>; rel="%s"`, p.srv.URL, r.URL.EscapedPath(), page, rel)
		}
		if n < p.total {
			w.Header().Add("Link", link("next", n+1))
		}
		w.Header().Add("Link", link("last", p.total))
		_, _ = fmt.Fprintf(w, "[%d,%d]", 2*n-1, 2*n)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pager) get(_ context.Context, path string, query url.Values) (*Response, error) {
	u := p.srv.URL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	return New("test", nil).Do(context.Background(), "Test", req)
}

func TestGetAllFollowsNextLinksUpToThePageCap(t *testing.T) {
	p := newPager(t, 3)
	got, err := GetAll[int](context.Background(), "Test", p.get, "/items", url.Values{"per_page": {"2"}}, 10)
	if err != nil || fmt.Sprint(got) != "[1 2 3 4 5 6]" || len(p.requests) != 3 {
		t.Fatalf("%v %v %v", got, err, p.requests)
	}
}

func TestGetAllKeepsAnEscapedPathEscaped(t *testing.T) {
	p := newPager(t, 2)
	got, err := GetAll[int](context.Background(), "Test", p.get, "/projects/a%2Fb/items", nil, 10)
	if err != nil || fmt.Sprint(got) != "[1 2 3 4]" {
		t.Fatalf("%v %v", got, err)
	}
	if want := "/projects/a%2Fb/items?page=2"; p.requests[1] != want {
		t.Fatalf("second request %q, want %q", p.requests[1], want)
	}
}

func TestGetAllStopsAtANon2xxWithWhatItHas(t *testing.T) {
	calls := 0
	get := func(context.Context, string, url.Values) (*Response, error) {
		calls++
		if calls == 2 {
			return &Response{Status: 404}, nil
		}
		return &Response{Status: 200, Header: http.Header{"Link": {`<http://x/items?page=2>; rel="next"`}}, Body: []byte("[1]")}, nil
	}
	got, err := GetAll[int](context.Background(), "Test", get, "/items", nil, 10)
	if err != nil || fmt.Sprint(got) != "[1]" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestGetAllNamesTheServiceWhenAnAnswerIsNotAList(t *testing.T) {
	get := func(context.Context, string, url.Values) (*Response, error) {
		return &Response{Status: 200, Body: []byte("{}")}, nil
	}
	if _, err := GetAll[int](context.Background(), "Test", get, "/items", nil, 10); err == nil {
		t.Fatal("want an error")
	}
}

func TestRateReadsEitherFamilyOfHeaders(t *testing.T) {
	var r Rate
	r.Read(http.Header{"X-Ratelimit-Limit": {"5000"}, "X-Ratelimit-Remaining": {"4990"}, "X-Ratelimit-Reset": {"1900000000"}})
	if r.Limit != 5000 || r.Remaining != 4990 || r.ResetAt.Unix() != 1900000000 {
		t.Fatalf("%+v", r)
	}
	// GitLab: its own names, and a reset given as seconds from now on some instances.
	r.Read(http.Header{"Ratelimit-Limit": {"600"}, "Ratelimit-Remaining": {"0"}, "Ratelimit-Reset": {"30"}})
	if r.Limit != 600 || r.Remaining != 0 || time.Until(r.ResetAt) < 20*time.Second || time.Until(r.ResetAt) > 31*time.Second {
		t.Fatalf("%+v", r)
	}
	// Nothing sent: nothing changes.
	before := r
	r.Read(http.Header{})
	if r != before {
		t.Fatalf("%+v became %+v", before, r)
	}
}
