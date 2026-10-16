// Package github reads a watch's pull requests, and its specs where the
// tracker is the repository's own issues, from the GitHub REST API; see
// docs/reference/sources.md, "GitHub".
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/application"
)

// API is GitHub's REST root.
const API = "https://api.github.com"

type client struct {
	http  *httpread.Client
	base  string
	token string
	rate  application.RateInfo
	at    time.Time // the service's time, from the first response's Date
}

func (c *client) get(ctx context.Context, path string, query url.Values, etag string) (*httpread.Response, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.http.Do(ctx, "GitHub", req)
	if resp != nil {
		c.readRate(resp.Header)
		if c.at.IsZero() {
			if t, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
				c.at = t.UTC()
			}
		}
	}
	if err != nil {
		return nil, err
	}
	switch {
	case resp.Status == http.StatusNotModified, resp.Status/100 == 2:
		return resp, nil
	case resp.Status == http.StatusNotFound || resp.Status == http.StatusGone:
		return resp, nil
	case resp.Status >= 500:
		return nil, httpread.NewError("unreachable", fmt.Sprintf("GitHub answered %d to GET %s", resp.Status, path),
			"GitHub's status page says whether it is down; wakewire tries again on the next poll")
	}
	return nil, httpread.NewError("unreachable", fmt.Sprintf("GitHub answered %d to GET %s", resp.Status, path), "`wakewire doctor`")
}

func (c *client) readRate(h http.Header) {
	if n, err := strconv.Atoi(h.Get("X-Ratelimit-Limit")); err == nil {
		c.rate.Limit = n
	}
	if n, err := strconv.Atoi(h.Get("X-Ratelimit-Remaining")); err == nil {
		c.rate.Remaining = n
	}
	if n, err := strconv.ParseInt(h.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
		c.rate.ResetAt = time.Unix(n, 0).UTC()
	}
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// getAll follows a list's next links, up to pages pages, decoding each
// page's items into out.
func getAll[T any](ctx context.Context, c *client, path string, query url.Values, pages int) ([]T, error) {
	var all []T
	for i := 0; i < pages; i++ {
		resp, err := c.get(ctx, path, query, "")
		if err != nil {
			return nil, err
		}
		if resp.Status/100 != 2 {
			return all, nil
		}
		var page []T
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, fmt.Errorf("GitHub's answer to %s: %w", path, err)
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
		path, query = next.Path, next.Query()
	}
	return all, nil
}
