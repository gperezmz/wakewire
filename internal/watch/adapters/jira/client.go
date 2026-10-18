// Package jira reads a watch's specs from a Jira Cloud project through the
// REST API; see docs/reference/sources.md, "Jira Cloud".
package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/v2/internal/watch/application"
)

type client struct {
	http  *httpread.Client
	base  string // https://<site>
	email string
	token string
	rate  application.RateInfo
	at    time.Time // the service's time, from the first response's Date
}

// do sends one request. A body, when given, is JSON. Any 2xx comes back;
// so does a 404, for the caller to judge; every other status is an error.
func (c *client) do(ctx context.Context, method, path string, query url.Values, body any) (*httpread.Response, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	var req *http.Request
	var err error
	if rd != nil {
		req, err = http.NewRequest(method, u, rd)
	} else {
		req, err = http.NewRequest(method, u, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(c.email, c.token)
	resp, err := c.http.Do(ctx, "Jira", req)
	if resp != nil {
		c.readHeaders(resp.Header)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case resp.Status/100 == 2, resp.Status == http.StatusNotFound:
		return resp, nil
	}
	hint := "`wakewire doctor`"
	if resp.Status >= 500 {
		hint = "Atlassian's status page says whether Jira is down; wakewire tries again on the next poll"
	}
	msg := fmt.Sprintf("Jira answered %d to %s %s", resp.Status, method, path)
	if why := errorMessages(resp.Body); why != "" {
		msg += ": " + why
	}
	return nil, httpread.NewError("unreachable", msg, hint)
}

// errorMessages reads the reason Jira gives with a refusal.
func errorMessages(body []byte) string {
	var doc struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return ""
	}
	parts := doc.ErrorMessages
	for _, v := range doc.Errors {
		parts = append(parts, v)
	}
	return strings.Join(parts, "; ")
}

// readHeaders takes the service's time and its warning from a response.
// Jira publishes no budget: only X-RateLimit-NearLimit says one is close.
func (c *client) readHeaders(h http.Header) {
	if strings.EqualFold(h.Get("X-RateLimit-NearLimit"), "true") {
		c.rate.NearLimit = true
	}
	if c.at.IsZero() {
		if t, err := http.ParseTime(h.Get("Date")); err == nil {
			c.at = t.UTC()
		}
	}
}
