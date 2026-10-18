package cli

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// homeWithState makes a home folder holding the given bytes as state.db,
// and answers the path of state.db.
func homeWithState(t *testing.T, content []byte) string {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, "state.db")
	if err := os.WriteFile(state, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAKEWIRE_HOME", home)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	return state
}

// A state.db that does not open is the person's file to fix or move aside,
// not a bug in wakewire: its own code, the path named once, `paths` as the
// way to find it, and exit 1. The home view and doctor read what they can
// and name it in place.
func TestACorruptStateDBIsStateUnreadable(t *testing.T) {
	state := homeWithState(t, []byte("this is not a database, and it is long enough to be read as one header "+strings.Repeat("x", 200)))
	for _, args := range [][]string{{"status"}, {"ledger"}, {"prune"}, {"prune", "--dry-run"}} {
		code, out, errOut := runReal(append(args, "--json")...)
		e, _ := object(t, out)["error"].(map[string]any)
		if code != exitError || errOut != "" || e["code"] != "state_unreadable" {
			t.Errorf("%v: exit %d stderr %q\n%s", args, code, errOut, out)
			continue
		}
		if n := strings.Count(e["message"].(string), state); n != 1 {
			t.Errorf("%v: the path is named %d times: %v", args, n, e["message"])
		}
		if !strings.Contains(e["hint"].(string), "wakewire paths") {
			t.Errorf("%v: the hint does not name `wakewire paths`: %v", args, e["hint"])
		}
	}
	// In TOON the same refusal carries its code and no internal-bug text.
	code, out, _ := runReal("status")
	if code != exitError || !strings.Contains(out, "code: state_unreadable\n") || strings.Contains(out, "bug in wakewire") {
		t.Errorf("exit %d\n%s", code, out)
	}
	if code, out, _ = runReal(); code != exitOK || !strings.Contains(out, "state: \"unreadable: ") {
		t.Errorf("home view: exit %d\n%s", code, out)
	}
	code, out, _ = runReal("doctor")
	for _, want := range []string{"ok: false", "state,fail,", `source,skipped,"not checked: state.db did not open"`, "state,state_unreadable,"} {
		if code != exitError || !strings.Contains(out, want) {
			t.Errorf("doctor: exit %d, lacks %q:\n%s", code, want, out)
		}
	}
}

// A state.db written by a newer release keeps its own code through the real
// wiring, for a real open and a dry one.
func TestAStateDBFromANewerReleaseIsStateNewer(t *testing.T) {
	state := homeWithState(t, nil)
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	// Any command that writes makes a state.db at the schema this binary speaks.
	if code, out, _ := runReal("prune"); code != exitOK {
		t.Fatalf("prune: exit %d\n%s", code, out)
	}
	raw, err := sql.Open("sqlite", state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE meta SET value='999' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	for _, args := range [][]string{{"status"}, {"status", "--json"}, {"prune", "--dry-run"}} {
		code, out, _ := runReal(args...)
		if code != exitError || !strings.Contains(out, "state_newer") || strings.Contains(out, "state_unreadable") {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
}

// A home folder that cannot be made is its own refusal, naming the way to
// find where it is.
func TestAHomeFolderThatCannotBeMadeIsHomeUnwritable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAKEWIRE_HOME", filepath.Join(file, "home")) // a folder under a file
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	code, out, errOut := runReal("prune", "--json")
	e, _ := object(t, out)["error"].(map[string]any)
	if code != exitError || errOut != "" || e["code"] != "home_unwritable" || !strings.Contains(e["message"].(string), filepath.Join(file, "home")) ||
		!strings.Contains(e["hint"].(string), "wakewire paths") {
		t.Fatalf("exit %d stderr %q\n%s", code, errOut, out)
	}
}
