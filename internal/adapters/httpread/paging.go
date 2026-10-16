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

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// GetAll follows a list's next links, up to pages pages, decoding each page's
// JSON array into one slice. A status outside 2xx ends the list with what was
// read. service names the service in errors ("GitHub").
func GetAll[T any](ctx context.Context, service string, get Get, path string, query url.Values, pages int) ([]T, error) {
	var all []T
	for range pages {
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
		m := nextLink.FindStringSubmatch(resp.Header.Get("Link"))
		if m == nil {
			break
		}
		next, err := url.Parse(m[1])
		if err != nil {
			break
		}
		// The escaped path: GitLab names a project by its path, slash escaped.
		path, query = next.EscapedPath(), next.Query()
	}
	return all, nil
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
