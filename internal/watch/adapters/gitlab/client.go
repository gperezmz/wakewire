// Package gitlab reads a watch's merge requests, and its specs where the
// tracker is the project's own issues, from the GitLab REST API; see
// docs/reference/sources.md, "GitLab".
package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/v2/internal/watch/application"
)

// apiRoot is where GitLab serves its REST API, under the instance's origin.
const apiRoot = "/api/v4"

type client struct {
	http  *httpread.Client
	base  string // the instance's origin: scheme and host
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
	req.Header.Set("Accept", "application/json")
	req.Header.Set("PRIVATE-TOKEN", c.token)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.http.Do(ctx, "GitLab", req)
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
	case resp.Status == http.StatusNotFound:
		return resp, nil
	case resp.Status >= 500:
		return nil, httpread.NewError("unreachable", fmt.Sprintf("GitLab answered %d to GET %s", resp.Status, path),
			"GitLab's status page says whether it is down; wakewire tries again on the next poll")
	}
	return nil, httpread.NewError("unreachable", fmt.Sprintf("GitLab answered %d to GET %s", resp.Status, path), "`wakewire doctor`")
}

// readRate reads the budget GitLab reports. An instance with rate limits
// off sends none of these headers, and the rate stays zero.
func (c *client) readRate(h http.Header) {
	r := httpread.Rate{Limit: c.rate.Limit, Remaining: c.rate.Remaining, ResetAt: c.rate.ResetAt}
	r.Read(h)
	c.rate.Limit, c.rate.Remaining, c.rate.ResetAt = r.Limit, r.Remaining, r.ResetAt
}

// getAll follows a list's next links, up to pages pages, decoding each
// page's items into one slice.
func getAll[T any](ctx context.Context, c *client, path string, query url.Values, pages int) ([]T, error) {
	return httpread.GetAll[T](ctx, "GitLab", func(ctx context.Context, path string, query url.Values) (*httpread.Response, error) {
		return c.get(ctx, path, query, "")
	}, path, query, pages)
}
