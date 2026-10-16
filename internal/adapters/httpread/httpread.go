// Package httpread is the one HTTP client wakewire reaches the services
// through. It only reads: it refuses every method but GET and HEAD, save the
// two Jira searches that are read-only POSTs, so no code path can write to a
// service. Its errors carry the CLI's error codes and never a token.
package httpread

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RequestTimeout bounds one request; see configuration.md, "Fixed in v1".
const RequestTimeout = 15 * time.Second

// readOnlyPosts are the POST endpoints that read and never write.
var readOnlyPosts = []string{
	"/rest/api/3/search/jql",
	"/rest/api/3/changelog/bulkfetch",
}

// Error is a service failure, coded as the CLI's error table codes it.
type Error struct {
	code, message, hint string
	Status              int
	ResetAt             time.Time
}

func (e *Error) Error() string { return e.message }
func (e *Error) Code() string  { return e.code }
func (e *Error) Hint() string  { return e.hint }

// Reset is when a spent budget refills; zero when the service did not say or
// the error is not about a rate limit.
func (e *Error) Reset() time.Time { return e.ResetAt }

// NewError builds a coded service error.
func NewError(code, message, hint string) *Error {
	return &Error{code: code, message: message, hint: hint}
}

// Client sends read-only requests.
type Client struct {
	http      *http.Client
	userAgent string
}

// New returns a client whose transport refuses writes.
func New(userAgent string, base http.RoundTripper) *Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &Client{
		http:      &http.Client{Timeout: RequestTimeout, Transport: readOnly{base}},
		userAgent: userAgent,
	}
}

// ErrWrite is what the transport answers a request that would write.
var ErrWrite = errors.New("wakewire only reads")

type readOnly struct{ next http.RoundTripper }

func (r readOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if !Allowed(req.Method, req.URL.Path) {
		return nil, fmt.Errorf("%w: %s %s refused", ErrWrite, req.Method, req.URL.Path)
	}
	return r.next.RoundTrip(req)
}

// Allowed says whether a request reads only.
func Allowed(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		return true
	case http.MethodPost:
		for _, p := range readOnlyPosts {
			if strings.HasSuffix(path, p) {
				return true
			}
		}
	}
	return false
}

// Response is what a service answered.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do sends req with the service's authorization already set on it and
// returns the response. service names the service in errors ("GitHub").
// A 401 or 403 is token_refused, no answer is unreachable, a spent budget
// rate_limited; any other status at or above 400 is returned as a Response
// for the caller to judge.
func (c *Client) Do(ctx context.Context, service string, req *http.Request) (*Response, error) {
	req = req.WithContext(ctx)
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		var ue interface{ Timeout() bool }
		msg := fmt.Sprintf("%s did not answer %s %s", service, req.Method, req.URL.Path)
		if errors.As(err, &ue) && ue.Timeout() {
			msg += " within " + RequestTimeout.String()
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			msg = fmt.Sprintf("%s did not answer %s %s before the poll's budget ran out", service, req.Method, req.URL.Path)
		}
		if errors.Is(err, ErrWrite) {
			return nil, NewError("invalid_value", err.Error(), "this is a bug in wakewire: report it")
		}
		return nil, NewError("unreachable", msg, "check the network, then `wakewire doctor`")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, NewError("unreachable", fmt.Sprintf("%s's answer to %s was cut off", service, req.URL.Path), "`wakewire doctor`")
	}
	r := &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}
	switch {
	case resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode == http.StatusForbidden && !rateLimited(resp):
		return r, &Error{
			code:    "token_refused",
			message: fmt.Sprintf("%s refused the token (%d)", service, resp.StatusCode),
			hint:    "`wakewire doctor` names the account; replace its token with `wakewire account add`",
			Status:  resp.StatusCode,
		}
	case resp.StatusCode == http.StatusTooManyRequests || rateLimited(resp):
		reset := resetTime(resp.Header)
		msg := fmt.Sprintf("%s's rate limit is spent", service)
		if !reset.IsZero() {
			msg += ", until " + reset.UTC().Format(time.RFC3339)
		}
		return r, &Error{code: "rate_limited", message: msg, hint: "wait for the reset; `wakewire status` shows it", Status: resp.StatusCode, ResetAt: reset}
	}
	return r, nil
}

// rateLimited says whether a 403 is a spent budget rather than a refused
// token: the primary limit reports no calls remaining, GitHub's secondary
// limit asks to retry later with calls remaining.
func rateLimited(resp *http.Response) bool {
	return resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("X-Ratelimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "")
}

// resetTime reads when a spent budget resets, from whichever header the
// service sends: Retry-After (seconds), X-RateLimit-Reset or RateLimit-Reset
// (epoch seconds), or RateLimit-ResetTime (an HTTP date).
func resetTime(h http.Header) time.Time {
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return time.Now().Add(time.Duration(n) * time.Second)
		}
	}
	for _, k := range []string{"X-Ratelimit-Reset", "Ratelimit-Reset"} {
		if s := h.Get(k); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				if n < 1e9 { // GitLab sends seconds from now on some instances
					return time.Now().Add(time.Duration(n) * time.Second)
				}
				return time.Unix(n, 0)
			}
		}
	}
	if s := h.Get("Ratelimit-Resettime"); s != "" {
		if t, err := http.ParseTime(s); err == nil {
			return t
		}
	}
	return time.Time{}
}
