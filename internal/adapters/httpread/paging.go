package httpread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// Get sends one GET for path and query and answers the response.
type Get func(ctx context.Context, path string, query url.Values) (*Response, error)

var (
	nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)
	lastLink = regexp.MustCompile(`<([^>]+)>;\s*rel="last"`)
)

// GetAll follows a list's next links, up to pages pages, decoding each
// page's JSON array into one slice. A status outside 2xx ends the list with
// what was read. service names the service in errors ("GitHub"). It suits a
// list that comes newest first, where what is new is on the first pages.
func GetAll[T any](ctx context.Context, service string, get Get, path string, query url.Values, pages int) ([]T, error) {
	return getAll[T](ctx, service, get, path, query, pages, false)
}

// GetLast reads a list that comes oldest first and grows at its end, such as
// an item's timeline: the last pages pages of it, oldest first. A list of
// pages pages or fewer is read whole. A longer one is read from page 1 only
// to learn how many pages there are, and then from the page that makes the
// last pages: what happened since the last poll is at the end, and the
// oldest pages are what the cap may drop. A list whose Link names no last
// page is read from its start, as far as the cap goes.
func GetLast[T any](ctx context.Context, service string, get Get, path string, query url.Values, pages int) ([]T, error) {
	return getAll[T](ctx, service, get, path, query, pages, true)
}

func getAll[T any](ctx context.Context, service string, get Get, path string, query url.Values, pages int, fromEnd bool) ([]T, error) {
	var all []T
	skipped := false
	for n := 0; n < pages; n++ {
		resp, err := get(ctx, path, query)
		if err != nil {
			return nil, err
		}
		if resp.Status/100 != 2 {
			return all, nil
		}
		var page []T
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, fmt.Errorf("%s's answer to %s: %w", service, path, err)
		}
		all = append(all, page...)
		link := resp.Header.Get("Link")
		m := nextLink.FindStringSubmatch(link)
		if m == nil {
			break
		}
		next, err := url.Parse(m[1])
		if err != nil {
			break
		}
		if fromEnd && n == 0 && !skipped {
			if last, from, ok := tail(link, pages); ok {
				// More than the cap allows: drop page 1 and read the last pages.
				all, skipped, n = nil, true, -1
				path, query = last.EscapedPath(), last.Query()
				query.Set("page", strconv.Itoa(from))
				continue
			}
		}
		// The escaped path: GitLab names a project by its path, slash escaped.
		path, query = next.EscapedPath(), next.Query()
	}
	return all, nil
}

// tail says, from a Link header, whether the list is longer than pages pages
// and, if so, the page its last pages pages start at and the URL of its last.
func tail(link string, pages int) (last *url.URL, from int, ok bool) {
	m := lastLink.FindStringSubmatch(link)
	if m == nil {
		return nil, 0, false
	}
	last, err := url.Parse(m[1])
	if err != nil {
		return nil, 0, false
	}
	total, err := strconv.Atoi(last.Query().Get("page"))
	if err != nil || total <= pages {
		return nil, 0, false
	}
	return last, total - pages + 1, true
}

// Rate is a service's budget as its responses report it.
type Rate struct {
	Limit, Remaining int
	ResetAt          time.Time
}

// Read updates r with whatever the headers carry, whichever family the
// service sends (X-RateLimit-* or RateLimit-*), and leaves the rest as it was.
func (r *Rate) Read(h http.Header) {
	if n, ok := number(h, "X-Ratelimit-Limit", "Ratelimit-Limit"); ok {
		r.Limit = n
	}
	if n, ok := number(h, "X-Ratelimit-Remaining", "Ratelimit-Remaining"); ok {
		r.Remaining = n
	}
	if t := resetTime(h); !t.IsZero() {
		r.ResetAt = t.UTC()
	}
}

func number(h http.Header, keys ...string) (int, bool) {
	for _, k := range keys {
		if n, err := strconv.Atoi(h.Get(k)); err == nil {
			return n, true
		}
	}
	return 0, false
}
