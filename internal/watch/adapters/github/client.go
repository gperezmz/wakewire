// Package github reads a watch's pull requests, and its specs where the
// tracker is the repository's own issues, from the GitHub REST API; see
// docs/reference/sources.md, "GitHub".
package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	r := httpread.Rate{Limit: c.rate.Limit, Remaining: c.rate.Remaining, ResetAt: c.rate.ResetAt}
	r.Read(h)
	c.rate.Limit, c.rate.Remaining, c.rate.ResetAt = r.Limit, r.Remaining, r.ResetAt
}

// getLast reads the last pages pages of a list that comes oldest first and
// grows at its end, so that a busy item's new activity is never past the cap.
func getLast[T any](ctx context.Context, c *client, path string, query url.Values, pages int) ([]T, error) {
	return httpread.GetLast[T](ctx, "GitHub", func(ctx context.Context, path string, query url.Values) (*httpread.Response, error) {
		return c.get(ctx, path, query, "")
	}, path, query, pages)
}
