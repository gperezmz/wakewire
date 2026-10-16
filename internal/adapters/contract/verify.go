package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Shape compares the JSON shape of a recorded body with a live one: every
// key the recording holds must be in the live body with the same JSON
// type, and arrays compare their first elements. A null on either side
// matches anything, since a field may be empty. It answers each
// difference as a path and what was found.
func Shape(recorded, live []byte) []string {
	var a, b any
	if json.Unmarshal(recorded, &a) != nil {
		return nil
	}
	if err := json.Unmarshal(live, &b); err != nil {
		return []string{"$: the live body is not JSON"}
	}
	var out []string
	shape("$", a, b, &out)
	sort.Strings(out)
	return out
}

func kind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func shape(path string, rec, live any, out *[]string) {
	if rec == nil || live == nil {
		return
	}
	if kind(rec) != kind(live) {
		*out = append(*out, fmt.Sprintf("%s: recorded %s, live %s", path, kind(rec), kind(live)))
		return
	}
	switch r := rec.(type) {
	case map[string]any:
		l := live.(map[string]any)
		for k, v := range r {
			lv, ok := l[k]
			if !ok {
				*out = append(*out, fmt.Sprintf("%s.%s: gone", path, k))
				continue
			}
			shape(path+"."+k, v, lv, out)
		}
	case []any:
		l := live.([]any)
		if len(r) > 0 && len(l) > 0 {
			shape(path+"[0]", r[0], l[0], out)
		}
	}
}

// Live sends a contract's requests to the real service and answers, per
// interaction, how its live response's shape differs from the recording.
// base is the service's root URL; authorize sets the request's
// credentials; restore undoes the scrubbing of a path or query value.
func Live(c Contract, base string, client *http.Client, authorize func(*http.Request), restore func(string) string) (map[int][]string, error) {
	diffs := map[int][]string{}
	seen := map[string]bool{}
	for i, it := range c.Interactions {
		if it.Response.Status/100 != 2 || len(it.Response.Body) == 0 {
			continue
		}
		k := it.Request.key()
		if seen[k] {
			continue // the same request again, later in the run: one look is enough
		}
		seen[k] = true
		u := strings.TrimSuffix(base, "/") + restore(it.Request.Path)
		var q []string
		for k, v := range it.Request.Query {
			q = append(q, k+"="+restore(v))
		}
		if len(q) > 0 {
			sort.Strings(q)
			u += "?" + strings.Join(q, "&")
		}
		var body io.Reader
		if len(it.Request.Body) > 0 {
			body = bytes.NewReader([]byte(restore(string(it.Request.Body))))
		}
		req, err := http.NewRequest(it.Request.Method, u, body)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		authorize(req)
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", it, err)
		}
		live, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			diffs[i] = []string{fmt.Sprintf("status %d", resp.StatusCode)}
			continue
		}
		if d := Shape(it.Response.Body, live); len(d) > 0 {
			diffs[i] = d
		}
	}
	return diffs, nil
}
