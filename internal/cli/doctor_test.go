package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	accountapp "github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// The budget doctor warns about is the one at which a poll stops reading
// the source: the same constant, not two. The warning says when it resets
// relative to now and names a fix that is a command to run.
func TestDoctorWarnsOfTheBudgetExactlyWhereAPollStopsReading(t *testing.T) {
	for _, c := range []struct {
		remaining int
		warns     bool
	}{{700, false}, {260, false}, {240, true}, {0, true}} {
		w := newFakeWorld()
		w.watch.statuses = []application.WatchStatus{{Watch: "github.com/o/x", Sources: []application.SourceStatus{{Name: application.SourceForge, Service: "GitHub",
			Rate: &application.RateInfo{Remaining: c.remaining, Limit: 5000, ResetAt: time.Now().Add(20*time.Minute + 30*time.Second)}}}}}
		code, out, _ := w.run(t, "doctor")
		if code != 0 {
			t.Fatalf("%d left: exit %d\n%s", c.remaining, code, out)
		}
		if got := strings.Contains(out, "budget,warn,"); got != c.warns {
			t.Errorf("%d of 5000 left: warned %v\n%s", c.remaining, got, out)
		}
		want := fmt.Sprintf("GitHub for github.com/o/x has %d of 5000 requests left; it resets in 20m", c.remaining)
		if c.warns && (!strings.Contains(out, want) || !strings.Contains(out, "wakewire interval <duration>") || strings.Contains(out, "until")) {
			t.Errorf("%d left: the warning is not %q with the interval fix:\n%s", c.remaining, want, out)
		}
	}
}

func oneAccount() []accountapp.AccountView {
	return []accountapp.AccountView{{Name: "personal", Host: "github.com", Service: accountapp.GitHub, Owners: []string{"o"}, TokenSource: "stored"}}
}

// doctor's exit code is its answer to a script: 0 when every check passed
// or only warned, 1 when any check failed, whether something is not set up
// or a service failed: the failing check's code says which. It changes
// nothing, so it opens state.db dry.
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
		}, exitError},
		{"an account has no token", func(w *fakeWorld) {
			w.acc.views = oneAccount()
			w.acc.checkErr = accountapp.NewError(accountapp.CodeNoToken, "$GH is empty", "set it")
		}, exitError},
		{"a service refuses the token", func(w *fakeWorld) {
			w.acc.views, w.acc.checkErr = oneAccount(), refused
		}, exitError},
		{"a failure before a warning", func(w *fakeWorld) {
			w.acc.views, w.acc.checkErr, w.acc.unused = oneAccount(), refused, []string{"old"}
		}, exitError},
		{"the credentials do not parse", func(w *fakeWorld) {
			w.acc.unusedErr = accountapp.NewError(accountapp.CodeConfigInvalid, "credentials.toml does not parse", "fix line 3")
		}, exitError},
		{"the repository has no source section", func(w *fakeWorld) {
			w.acc.views = oneAccount()
			w.watch.foundErr = domain.Refuse("no_source", "no file has the section", "add it")
		}, exitError},
		{"a watch has a problem with a service", func(w *fakeWorld) {
			w.acc.views, w.watch.statuses = oneAccount(), problem("unreachable")
		}, exitError},
		{"a watch has a problem with a token", func(w *fakeWorld) {
			w.acc.views, w.watch.statuses = oneAccount(), problem("token_refused")
		}, exitError},
	} {
		w := newFakeWorld()
		c.setup(w)
		code, out, errOut := w.run(t, "doctor", "--json")
		if code != c.exit || errOut != "" || slices.Contains(w.openDry, false) {
			t.Errorf("%s: exit %d, want %d, stderr %q, opened with dry run %v\n%s", c.name, code, c.exit, errOut, w.openDry, out)
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
	for _, want := range []string{"ok: false", "account personal,fail,GitHub refused the token (401)",
		"fixes[1]{name,code,fix}:", "account personal,token_refused,replace it with wakewire account add"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if code != exitError {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// doctor checks this session's mod by the hello it last said: no hello and a
// mod of another protocol only warn, a mod that agrees is ok, and a state it
// cannot read is a failure.
func TestDoctorChecksTheModByItsHello(t *testing.T) {
	heard := time.Now().Add(-10 * time.Minute)
	for _, c := range []struct {
		name   string
		setup  func(f *fakeWatch)
		exit   int
		want   string
		absent string
	}{
		{"a mod that agrees", func(f *fakeWatch) {
			f.adapter = &application.Adapter{Session: "s1", Protocol: domain.Protocol, HeardAt: heard}
		}, exitOK, `mod,ok,"protocol 2, heard 10m ago"`, "fixes["},
		{"no hello", func(f *fakeWatch) {}, exitOK, `mod,warn,no wakewire mod has said hello for this session`,
			""},
		{"another protocol", func(f *fakeWatch) {
			f.adapter = &application.Adapter{Session: "s1", Protocol: 1, HeardAt: heard}
		}, exitOK, `mod,warn,"the mod speaks protocol 1, this binary 2"`, ""},
		{"a state it cannot read", func(f *fakeWatch) { f.adapterErr = domain.Refuse("internal", "database is locked", "retry") },
			exitError, `mod,fail,database is locked`, ""},
	} {
		w := newFakeWorld()
		w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
		c.setup(w.watch)
		code, out, errOut := w.run(t, "doctor")
		if code != c.exit || errOut != "" || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit %d (want %d), stderr %q, lacks %q:\n%s", c.name, code, c.exit, errOut, c.want, out)
		}
		if c.absent != "" && strings.Contains(out, c.absent) {
			t.Errorf("%s: a fix for a check that passed:\n%s", c.name, out)
		}
	}
	// With no session the check is skipped, not failed.
	w := newFakeWorld()
	if _, out, _ := w.run(t, "doctor"); !strings.Contains(out, "mod,skipped,not run from a Claude Code session") {
		t.Errorf("no session:\n%s", out)
	}
}

func TestDoctorWhenTheStateDoesNotOpenFailsItAndSkipsWhatNeedsIt(t *testing.T) {
	w := newFakeWorld()
	w.acc.views = oneAccount()
	w.acc.unused = []string{"old"}
	w.openErr = domain.Refuse("state_unreadable", "/h/state.db does not open: file is not a database", "move it aside; `wakewire paths` shows where it is")
	code, out, errOut := w.run(t, "doctor")
	if code != exitError || errOut != "" {
		t.Fatalf("exit %d stderr %q\n%s", code, errOut, out)
	}
	for _, want := range []string{"ok: false", "checks[8]{name,result,detail}:",
		"stored tokens,warn,", // the stored tokens were read before the state was opened
		"state,fail,", `source,skipped,"not checked: state.db did not open"`, `mod,skipped,"not checked: state.db did not open"`,
		`watches,skipped,"not checked: state.db did not open"`, `budget,skipped,"not checked: state.db did not open"`,
		"state,state_unreadable,"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "stored tokens") > strings.Index(out, "state,fail") {
		t.Errorf("the state is checked before the stored tokens:\n%s", out)
	}
	// Under --json each skipped check says why, and the run still fails.
	_, out, _ = w.run(t, "doctor", "--json")
	var got struct {
		OK     bool
		Checks []struct {
			Name, Detail string
			OK, Skipped  bool
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.OK {
		t.Fatalf("%v\n%s", err, out)
	}
	skipped := 0
	for _, c := range got.Checks {
		if c.Skipped {
			skipped++
			if c.Detail != "not checked: state.db did not open" {
				t.Errorf("%s: %q", c.Name, c.Detail)
			}
		}
	}
	if skipped != 4 {
		t.Errorf("%d checks skipped, want 4:\n%s", skipped, out)
	}
}

func TestDoctorWhenTheWatchesDoNotLoadFailsThemAndSkipsTheBudget(t *testing.T) {
	w := newFakeWorld()
	w.watch.statusErr = errors.New("database is locked")
	code, out, _ := w.run(t, "doctor")
	for _, want := range []string{"ok: false", "watches,fail,database is locked", `budget,skipped,"not checked: the watches did not load"`} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if code != exitError {
		t.Errorf("exit %d", code)
	}
	if strings.Contains(out, "budget,ok") || strings.Contains(out, "watches,ok") {
		t.Errorf("a check that did not run passed:\n%s", out)
	}
}
