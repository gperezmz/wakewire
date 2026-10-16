package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Scrubber replaces personal data before a recording is written: each
// Replace pair in every string, and every email address. Tokens never
// reach a recording, since request headers other than those named in
// Keep are not recorded at all.
type Scrubber struct {
	Replace [][2]string // real, placeholder; longest first is applied first
	// KeepRequest are the request headers the contract matches on.
	KeepRequest []string
	// KeepResponse are the response headers the client reads.
	KeepResponse []string
	// Drop are JSON keys removed from response bodies wherever they appear,
	// for fields that hold personal data the client never reads.
	Drop []string
}

var email = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// Text scrubs one string.
func (s Scrubber) Text(v string) string {
	pairs := append([][2]string(nil), s.Replace...)
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i][0]) > len(pairs[j][0]) })
	for _, p := range pairs {
		if p[0] != "" {
			v = strings.ReplaceAll(v, p[0], p[1])
		}
	}
	return email.ReplaceAllStringFunc(v, func(m string) string {
		if strings.HasSuffix(m, "@example.com") || strings.HasSuffix(m, "noreply.github.com") {
			return m
		}
		return "person@example.com"
	})
}

func (s Scrubber) value(v any) any {
	switch x := v.(type) {
	case string:
		return s.Text(x)
	case []any:
		for i := range x {
			x[i] = s.value(x[i])
		}
		return x
	case map[string]any:
		for _, k := range s.Drop {
			delete(x, k)
		}
		for k := range x {
			x[k] = s.value(x[k])
		}
		return x
	}
	return v
}

// Body scrubs a JSON body; a body that is not JSON is scrubbed as text.
func (s Scrubber) Body(b []byte) json.RawMessage {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		q, _ := json.Marshal(s.Text(string(b)))
		return q
	}
	out, _ := json.Marshal(s.value(v))
	return out
}

// Recorder is an http.RoundTripper that records what passes through it.
type Recorder struct {
	Next    http.RoundTripper
	Scrub   Scrubber
	mu      sync.Mutex
	records []Interaction
	Name    string // set by the test before each step, to label interactions
}

// RoundTrip sends the request and records it, scrubbed.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	resp, err := r.Next.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))

	u := *req.URL
	q := u.Query()
	for k, vs := range q {
		for i := range vs {
			vs[i] = r.Scrub.Text(vs[i])
		}
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	it := Interaction{Name: r.Name, Request: Request{Method: req.Method, Path: r.Scrub.Text(u.Path), Query: queryOf(&u)}}
	for _, h := range r.Scrub.KeepRequest {
		if v := req.Header.Get(h); v != "" {
			if it.Request.Headers == nil {
				it.Request.Headers = map[string]string{}
			}
			it.Request.Headers[h] = v
		}
	}
	if len(reqBody) > 0 {
		it.Request.Body = r.Scrub.Body(reqBody)
	}
	it.Response = Response{Status: resp.StatusCode, Body: r.Scrub.Body(body)}
	for _, h := range r.Scrub.KeepResponse {
		if v := resp.Header.Get(h); v != "" {
			if it.Response.Headers == nil {
				it.Response.Headers = map[string]string{}
			}
			it.Response.Headers[h] = r.Scrub.Text(v)
		}
	}
	r.mu.Lock()
	r.records = append(r.records, it)
	r.mu.Unlock()
	return resp, nil
}

// Contract answers what was recorded.
func (r *Recorder) Contract(service, note string) Contract {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Contract{Service: service, Note: note, Interactions: append([]Interaction(nil), r.records...)}
}
