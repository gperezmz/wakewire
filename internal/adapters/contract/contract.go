// Package contract holds the contracts between wakewire's source clients
// and the services they read, in the manner of consumer-driven contract
// tests: each contract is a list of interactions, a request the client
// makes and the response the service gave, recorded from the real service
// and scrubbed of tokens and personal data.
//
// The consumer side (Serve) replays a contract to the client in CI and
// fails on any request the contract does not hold, and on any interaction
// the client never made. The provider side (Verify) sends a contract's
// requests to the real service and fails where a field the recording holds
// is gone or has changed type. Record writes a contract from a real run.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

// Request is what the client sent. Headers holds only the headers the
// contract cares about, such as If-None-Match.
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

// Response is what the service answered.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

// Interaction is one request and its response.
type Interaction struct {
	Name     string   `json:"name,omitempty"`
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

// Contract is one recorded run against one service.
type Contract struct {
	Service string `json:"service"`
	// Note says what the run did, for whoever records it again.
	Note         string        `json:"note,omitempty"`
	Interactions []Interaction `json:"interactions"`
}

// Load reads a contract file.
func Load(path string) (Contract, error) {
	var c Contract
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

// Save writes a contract file, indented, bodies included.
func (c Contract) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// key is what a request is matched on.
func (r Request) key() string {
	var q []string
	for k, v := range r.Query {
		q = append(q, k+"="+v)
	}
	sort.Strings(q)
	return r.Method + " " + r.Path + "?" + strings.Join(q, "&")
}

func queryOf(u *url.URL) map[string]string {
	if len(u.Query()) == 0 {
		return nil
	}
	m := map[string]string{}
	for k, v := range u.Query() {
		m[k] = strings.Join(v, ",")
	}
	return m
}

// matches says whether a live request is this recorded one: the same
// method, path and query, every header the contract names, and the same
// JSON body where it has one.
func (r Request) matches(method string, u *url.URL, header func(string) string, body []byte) bool {
	live := Request{Method: method, Path: u.Path, Query: queryOf(u)}
	if live.key() != r.key() {
		return false
	}
	for k, v := range r.Headers {
		if header(k) != v {
			return false
		}
	}
	if len(r.Body) > 0 {
		var a, b any
		if json.Unmarshal(r.Body, &a) != nil || json.Unmarshal(body, &b) != nil {
			return bytes.Equal(r.Body, body)
		}
		return fmt.Sprint(a) == fmt.Sprint(b)
	}
	return true
}
