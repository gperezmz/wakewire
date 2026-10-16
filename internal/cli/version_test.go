package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/wiring"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(context.Background(), append([]string{"wakewire"}, args...),
		wiring.Build{Version: "v1.2.3", Commit: "abc123", Protocol: 1}, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionJSONReportsTagCommitAndProtocol(t *testing.T) {
	code, out, _ := run(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %q", out)
	}
	if got["version"] != "v1.2.3" || got["commit"] != "abc123" || got["protocol"] != float64(1) {
		t.Fatalf("got %v", got)
	}
}

func TestUsageErrorUnderJSONIsOneObjectAndExitsOne(t *testing.T) {
	code, out, errOut := run(t, "version", "--nope", "--json")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Error struct{ Code, Message, Hint string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Error.Code != "invalid_value" || got.Error.Hint == "" {
		t.Fatalf("stdout %q, stderr %q", out, errOut)
	}
}
