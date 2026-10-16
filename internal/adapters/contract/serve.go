package contract

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Serve replays a contract: each request is answered by the first unused
// interaction that matches it. A request no interaction matches fails the
// test, and so does an interaction the client never made, when the test
// ends.
func Serve(t *testing.T, c Contract) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	used := make([]bool, len(c.Interactions))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		for i, it := range c.Interactions {
			if used[i] || !it.Request.matches(r.Method, r.URL, r.Header.Get, body) {
				continue
			}
			used[i] = true
			for k, v := range it.Response.Headers {
				w.Header().Set(k, v)
			}
			if len(it.Response.Body) > 0 && w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(it.Response.Status)
			_, _ = w.Write(it.Response.Body)
			return
		}
		t.Errorf("%s contract: no interaction for %s %s (If-None-Match %q)", c.Service, r.Method, r.URL.RequestURI(),
			r.Header.Get("If-None-Match"))
		http.Error(w, "not in the contract", http.StatusNotImplemented)
	}))
	t.Cleanup(func() {
		srv.Close()
		mu.Lock()
		defer mu.Unlock()
		for i, u := range used {
			if !u {
				it := c.Interactions[i]
				t.Errorf("%s contract: interaction %d %s was never made: %s", c.Service, i, it.Name, it.Request.key())
			}
		}
	})
	return srv
}

// String names an interaction for messages.
func (it Interaction) String() string {
	return fmt.Sprintf("%s %s", it.Request.Method, it.Request.Path)
}
