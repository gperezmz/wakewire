package cli

import (
	"strings"
	"testing"
	"time"

	accountapp "github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/watch/application"
)

func statusWithRate(remaining, limit int) []application.WatchStatus {
	return []application.WatchStatus{{
		Watch: "github.com/o/x",
		Sources: []application.SourceStatus{{Name: application.SourceForge, Service: "GitHub",
			Rate: &application.RateInfo{Remaining: remaining, Limit: limit, ResetAt: time.Now().Add(time.Hour)}}},
	}}
}

// The budget doctor warns about is the one at which a poll stops reading
// the source: the same constant, not two.
func TestDoctorWarnsOfTheBudgetExactlyWhereAPollStopsReading(t *testing.T) {
	for _, c := range []struct {
		remaining int
		warns     bool
	}{{700, false}, {260, false}, {240, true}, {0, true}} {
		w := newFakeWorld()
		w.watch.statuses = statusWithRate(c.remaining, 5000)
		code, out, _ := w.run(t, "doctor")
		if code != 0 {
			t.Fatalf("%d left: exit %d\n%s", c.remaining, code, out)
		}
		if got := strings.Contains(out, "warn budget"); got != c.warns {
			t.Errorf("%d of 5000 left: warned %v\n%s", c.remaining, got, out)
		}
	}
}

func TestDoctorExitsNotSetUpWhenCredentialsDoNotParse(t *testing.T) {
	w := newFakeWorld()
	w.acc.unusedErr = accountapp.NewError(accountapp.CodeConfigInvalid, "credentials.toml does not parse", "fix line 3")
	code, out, errOut := w.run(t, "doctor", "--json")
	if errOut != "" {
		t.Fatalf("stderr must stay empty under --json: %s", errOut)
	}
	if code != exitNotSetUp {
		t.Fatalf("exit %d, want %d:\n%s", code, exitNotSetUp, out)
	}
	if got := object(t, out); got["ok"] != false {
		t.Fatalf("ok must be false:\n%s", out)
	}
}
