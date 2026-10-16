//go:build record

package jira_test

import (
	"os"
	"strings"
	"testing"
)

// TestE2EStep makes one change in the Jira sandbox, for an end-to-end run
// of a real session against it. WAKEWIRE_E2E names the change:
//
//	create:<summary>     a spec assigned to the token's owner; prints its key
//	comment:<KEY>:<text> a comment
//	done:<KEY>           the issue moved to Done
//
//	WAKEWIRE_JIRA_SANDBOX=<site>.atlassian.net WAKEWIRE_E2E=create:Export \
//	  go test -tags record -run TestE2EStep -v ./internal/watch/adapters/jira/
func TestE2EStep(t *testing.T) {
	step := os.Getenv("WAKEWIRE_E2E")
	if step == "" {
		t.Skip("set WAKEWIRE_E2E")
	}
	sb := newSandbox(t)
	verb, rest, _ := strings.Cut(step, ":")
	switch verb {
	case "create":
		key := sb.create(rest, []string{"type::spec"}, sb.accountID, nil)
		t.Logf("created %s", key)
	case "comment":
		key, text, _ := strings.Cut(rest, ":")
		sb.comment(key, text)
		t.Logf("commented on %s", key)
	case "done":
		sb.done(rest)
		t.Logf("%s is done", rest)
	default:
		t.Fatalf("unknown step %q", verb)
	}
}
