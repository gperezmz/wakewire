package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/watch/application"
)

// The next step a hint offers acts on the watch the command acted on, and on
// the identity that picked it, so it works from any folder and session.
func TestHintsNameTheWatchAndTheIdentityTheyActOn(t *testing.T) {
	w := newFakeWorld()
	w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
	since := time.Now().Add(-time.Minute)
	w.watch.registerRes = application.RegisterResult{Outcome: "registered", Watch: "github.com/o/x", Identity: "jdoe", Watched: true, Paused: &since, Adapter: "loaded"}

	// register has no --identity flag: it names the identity it resolved,
	// since two identities may watch one repository.
	_, out, _ := w.run(t, "register")
	for _, want := range []string{"Run `wakewire resume github.com/o/x --identity jdoe` to deliver what is held",
		"Run `wakewire status github.com/o/x --identity jdoe` to see the watch",
		"Run `wakewire pause github.com/o/x --identity jdoe --for <duration>` to hold its wakes"} {
		if !strings.Contains(out, want) {
			t.Errorf("register lacks %q:\n%s", want, out)
		}
	}

	_, out, _ = w.run(t, "pause", "--for", "1h", "--identity", "jdoe")
	if !strings.Contains(out, "Run `wakewire resume github.com/o/x --identity jdoe` to deliver everything held as one wake") {
		t.Errorf("pause:\n%s", out)
	}
	// Without --identity the hint names the watch alone.
	_, out, _ = w.run(t, "pause")
	if !strings.Contains(out, "Run `wakewire resume github.com/o/x` to deliver") || strings.Contains(out, "--identity") {
		t.Errorf("pause without an identity:\n%s", out)
	}
}

// A value with anything a shell would read is quoted in a hint, so the hint
// can be pasted as it stands.
func TestAHintQuotesWhatAShellWouldRead(t *testing.T) {
	for in, want := range map[string]string{"2h": "2h", "PR #41": "'PR #41'", "o'brien": `'o'\''brien'`, "a b": "'a b'", "": "''",
		"github.com/o/x": "github.com/o/x", "!41": "'!41'"} {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %s, want %s", in, got, want)
		}
	}
}
