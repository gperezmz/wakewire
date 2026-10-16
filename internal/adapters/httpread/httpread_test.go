package httpread

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOnlyReadsAreAllowed(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/repos/o/r/issues", true},
		{"HEAD", "/user", true},
		{"POST", "/rest/api/3/search/jql", true},
		{"POST", "/rest/api/3/changelog/bulkfetch", true},
		{"POST", "/repos/o/r/issues/1/comments", false},
		{"PATCH", "/repos/o/r/issues/1", false},
		{"PUT", "/x", false},
		{"DELETE", "/x", false},
		{"POST", "/rest/api/3/issue", false},
	}
	for _, c := range cases {
		if got := Allowed(c.method, c.path); got != c.want {
			t.Errorf("%s %s: got %v", c.method, c.path, got)
		}
	}
}

func TestAWriteNeverLeavesTheProcess(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	c := New("test", nil)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/repos/o/r/issues", nil)
	_, err := c.Do(context.Background(), "GitHub", req)
	if err == nil || hit {
		t.Fatalf("err %v, server hit %v", err, hit)
	}
}

func TestRefusedTokenIsCodedAndNamesNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/user", nil)
	req.Header.Set("Authorization", "Bearer sekrit-token-value")
	_, err := New("test", nil).Do(context.Background(), "GitHub", req)
	var e *Error
	if !errors.As(err, &e) || e.Code() != "token_refused" {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(e.Error()+e.Hint(), "sekrit") {
		t.Fatal("the error carries the token")
	}
}

func TestSpentBudgetIsRateLimitedWithReset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.Header().Set("X-Ratelimit-Reset", "1900000000")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/user", nil)
	_, err := New("test", nil).Do(context.Background(), "GitHub", req)
	var e *Error
	if !errors.As(err, &e) || e.Code() != "rate_limited" || e.ResetAt.Unix() != 1900000000 {
		t.Fatalf("got %v", err)
	}
	// The watch application skips a rate-limited source through this method.
	var r interface{ Reset() time.Time }
	if !errors.As(err, &r) || r.Reset().Unix() != 1900000000 {
		t.Fatalf("no Reset(): %v", err)
	}
}

// Go keeps a custom header such as PRIVATE-TOKEN across a redirect to another
// host, so the client must not follow one.
func TestARedirectToAnotherHostIsNotFollowedAndLeaksNoCredential(t *testing.T) {
	leaked := ""
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("PRIVATE-TOKEN") + r.Header.Get("Authorization")
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/stolen", http.StatusFound)
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/user", nil)
	req.Header.Set("PRIVATE-TOKEN", "sekrit-token-value")
	req.Header.Set("Authorization", "Bearer sekrit-token-value")
	_, err := New("test", nil).Do(context.Background(), "GitLab", req)
	var e *Error
	if !errors.As(err, &e) || e.Code() != "unreachable" || !strings.Contains(e.Error(), "another host") {
		t.Fatalf("got %v", err)
	}
	if leaked != "" {
		t.Fatal("a credential reached the other host")
	}
}

func TestARedirectWithinTheHostIsFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		_, _ = w.Write([]byte("moved"))
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/old", nil)
	resp, err := New("test", nil).Do(context.Background(), "GitHub", req)
	if err != nil || string(resp.Body) != "moved" {
		t.Fatalf("%v %v", resp, err)
	}
}

func TestASecondaryRateLimitIsRateLimitedNotARefusedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ratelimit-Remaining", "4000")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/user", nil)
	_, err := New("test", nil).Do(context.Background(), "GitHub", req)
	var e *Error
	if !errors.As(err, &e) || e.Code() != "rate_limited" || e.ResetAt.IsZero() {
		t.Fatalf("got %v", err)
	}
}
