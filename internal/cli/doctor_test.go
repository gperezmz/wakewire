package cli

import (
	"strings"
	"testing"
	"time"

	accountapp "github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
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

func oneAccount() []accountapp.AccountView {
	return []accountapp.AccountView{{Name: "personal", Host: "github.com", Service: accountapp.GitHub, Owners: []string{"o"}, TokenSource: "stored"}}
}

// doctor's exit code is its answer to a script: 0 when every check passed
// or only warned, 3 when something is not set up, 4 when a service failed.
func TestDoctorExitCodes(t *testing.T) {
	refused := accountapp.NewError("token_refused", "GitHub refused the token (401)", "replace it")
	problem := func(code string) []application.WatchStatus {
		return []application.WatchStatus{{Watch: "github.com/o/x", Problems: []application.ProblemView{{Code: code, Text: "not watching", Fix: "fix it"}}}}
	}
	for _, c := range []struct {
		name  string
		setup func(w *fakeWorld)
		exit  int
	}{
		{"everything passes", func(w *fakeWorld) { w.acc.views = oneAccount() }, 0},
		{"a warning only", func(w *fakeWorld) {
			w.acc.views, w.acc.unused = oneAccount(), []string{"old"}
		}, 0},
		{"the configuration does not parse", func(w *fakeWorld) {
			w.acc.listErr = accountapp.NewError(accountapp.CodeConfigInvalid, "config.toml does not parse", "fix it")
		}, exitNotSetUp},
		{"an account has no token", func(w *fakeWorld) {
			w.acc.views = oneAccount()
			w.acc.checkErr = accountapp.NewError(accountapp.CodeNoToken, "$GH is empty", "set it")
		}, exitNotSetUp},
		{"a service refuses the token", func(w *fakeWorld) {
			w.acc.views, w.acc.checkErr = oneAccount(), refused
		}, exitService},
		{"the repository has no source section", func(w *fakeWorld) {
			w.acc.views = oneAccount()
			w.watch.foundErr = domain.Refuse("no_source", "no file has the section", "add it")
		}, exitNotSetUp},
		{"a watch has a problem with a service", func(w *fakeWorld) {
			w.acc.views, w.watch.statuses = oneAccount(), problem("unreachable")
		}, exitService},
		{"a watch has a problem with a token", func(w *fakeWorld) {
			w.acc.views, w.watch.statuses = oneAccount(), problem("token_refused")
		}, exitService},
	} {
		w := newFakeWorld()
		c.setup(w)
		code, out, _ := w.run(t, "doctor", "--json")
		if code != c.exit {
			t.Errorf("%s: exit %d, want %d\n%s", c.name, code, c.exit, out)
		}
		if got := object(t, out)["ok"]; got != (c.exit == 0) {
			t.Errorf("%s: ok is %v with exit %d", c.name, got, code)
		}
	}
}

func TestDoctorSaysWhatToDoForEachFailure(t *testing.T) {
	w := newFakeWorld()
	w.acc.views = oneAccount()
	w.acc.checkErr = accountapp.NewError("token_refused", "GitHub refused the token (401)", "replace it with wakewire account add")
	code, out, _ := w.run(t, "doctor")
	if code != exitService || !strings.Contains(out, "FAIL account personal: GitHub refused the token (401)") ||
		!strings.Contains(out, "fix: replace it with wakewire account add") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
