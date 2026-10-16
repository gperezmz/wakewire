package httpread

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
}
