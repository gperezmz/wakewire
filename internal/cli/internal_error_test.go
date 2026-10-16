package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestAnErrorNothingNamesIsReportedAsInternal(t *testing.T) {
	w := newFakeWorld()
	w.watch.statusErr = errors.New("database is locked")
	code, out, _ := w.run(t, "status", "--json")
	if code != exitUsage || errorCode(t, out) != "internal" {
		t.Fatalf("exit %d\n%s", code, out)
	}
	e := object(t, out)["error"].(map[string]any)
	if e["message"] != "database is locked" || !strings.Contains(e["hint"].(string), "bug in wakewire") {
		t.Fatalf("%v", e)
	}
	// Without --json the same two lines go to stderr.
	w = newFakeWorld()
	w.watch.statusErr = errors.New("database is locked")
	code, out, errOut := w.run(t, "status")
	if code != exitUsage || out != "" || !strings.Contains(errOut, "database is locked") || !strings.Contains(errOut, "report it") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

func TestAUsageMistakeIsStillInvalidValue(t *testing.T) {
	w := newFakeWorld()
	code, out, _ := w.run(t, "pause", "--nonsense", "--json")
	if code != exitUsage || errorCode(t, out) != "invalid_value" {
		t.Fatalf("exit %d\n%s", code, out)
	}
	code, out, _ = w.run(t, "changes", "x", "--json")
	if code != exitUsage || errorCode(t, out) != "invalid_value" {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestEveryCodeInTheErrorTableHasAnExit(t *testing.T) {
	for _, code := range []string{"invalid_value", "internal", "no_session", "no_source", "source_incomplete", "no_account", "no_stored_token",
		"config_invalid", "no_token", "account_exists", "account_conflict", "no_such_account", "held", "not_registered",
		"token_refused", "unreachable", "rate_limited"} {
		if _, ok := exitFor[code]; !ok {
			t.Errorf("%s has no exit code", code)
		}
	}
}
