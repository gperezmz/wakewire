// Package gitlab reads a watch's merge requests, and its specs where the
// tracker is the project's own issues, from the GitLab REST API; see
// docs/reference/sources.md, "GitLab".
package gitlab

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
	if n, err := strconv.Atoi(h.Get("RateLimit-Limit")); err == nil {
		c.rate.Limit = n
	}
	if n, err := strconv.Atoi(h.Get("RateLimit-Remaining")); err == nil {
		c.rate.Remaining = n
	}
	if n, err := strconv.ParseInt(h.Get("RateLimit-Reset"), 10, 64); err == nil {
		if n < 1e9 { // seconds from now, as some instances send it
			c.rate.ResetAt = time.Now().UTC().Add(time.Duration(n) * time.Second)
		} else {
			c.rate.ResetAt = time.Unix(n, 0).UTC()
		}
	} else if t, err := http.ParseTime(h.Get("RateLimit-ResetTime")); err == nil {
		c.rate.ResetAt = t.UTC()
	}
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// getAll follows a list's next links, up to pages pages, decoding each
// page's items into out.
func getAll[T any](ctx context.Context, c *client, path string, query url.Values, pages int) ([]T, error) {
	var all []T
	for range pages {
		resp, err := c.get(ctx, path, query, "")
		if err != nil {
			return nil, err
		}
		if resp.Status/100 != 2 {
			return all, nil
		}
		var page []T
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, fmt.Errorf("GitLab's answer to %s: %w", path, err)
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
		path, query = next.EscapedPath(), next.Query()
	}
	return all, nil
}
