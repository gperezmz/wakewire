package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// A busy item has more timeline pages than the source reads: the pages it
// reads must be the newest, or what happened since the last poll is never
// seen.
func TestABusyItemsTimelineIsReadFromItsEnd(t *testing.T) {
	const total = 6
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/issues/5/timeline" {
			http.NotFound(w, r)
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("page"))
		n = max(n, 1)
		link := fmt.Sprintf(`<%s%s?per_page=100&page=%d>; rel="last"`, srv.URL, r.URL.Path, total)
		if n < total {
			link = fmt.Sprintf(`<%s%s?per_page=100&page=%d>; rel="next", `, srv.URL, r.URL.Path, n+1) + link
		}
		w.Header().Set("Link", link)
		_, _ = fmt.Fprintf(w, `[{"id":%d,"event":"closed","created_at":"2026-10-16T10:00:00Z"}]`, n)
	}))
	defer srv.Close()
	repo, _ := domain.ParseRepo("github.com/o/r")
	s := &Source{HTTP: httpread.New("test", nil), Base: srv.URL, Token: "t", Repo: repo, Name: "a", Pages: 2}
	c := &client{http: s.HTTP, base: srv.URL, token: "t"}
	item := domain.Item{Ref: "#5", Kind: domain.ItemIssue}
	o, err := s.observe(context.Background(), c, application.PollInput{}, item, domain.Snapshot{}, true)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range o.Activities {
		ids = append(ids, a.ID)
	}
	if got := strings.Join(ids, " "); got != "ev:5 ev:6" {
		t.Fatalf("read %s, want the last two pages", got)
	}
}
