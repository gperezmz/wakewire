package cli

import (
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/watch/application"
)

// pause, resume and interval say what they did, or under --dry-run what they
// would do, in one field, outcome; the dry run is not a field of its own.
func TestPauseResumeAndIntervalSayWhatTheyDidOrWouldDo(t *testing.T) {
	pause := func(r application.PauseResult) func(*fakeWorld) { return func(w *fakeWorld) { w.watch.pause = &r } }
	interval := func(already bool) func(*fakeWorld) {
		return func(w *fakeWorld) {
			w.watch.interval = &application.IntervalResult{Watch: "github.com/o/x", Interval: "1m0s", Already: already}
		}
	}
	const set = "watch: github.com/o/x\ninterval: 1m0s\ndefault: false\n"
	for _, c := range []struct {
		name  string
		args  []string
		setup func(*fakeWorld)
		want  string
	}{
		{"pause", []string{"pause"}, pause(application.PauseResult{Watch: "github.com/o/x", Paused: true}), "outcome: paused\n"},
		{"pause, dry run", []string{"pause", "--dry-run"}, pause(application.PauseResult{Watch: "github.com/o/x", Paused: true}), "outcome: would pause\n"},
		{"pause, already", []string{"pause"}, pause(application.PauseResult{Watch: "github.com/o/x", Paused: true, Already: true}), "outcome: already paused (no-op)\n"},
		{"pause, already, dry run", []string{"pause", "--dry-run"}, pause(application.PauseResult{Watch: "github.com/o/x", Paused: true, Already: true}), "outcome: already paused (no-op)\n"},
		{"resume", []string{"resume"}, pause(application.PauseResult{Watch: "github.com/o/x", Released: 3}), "outcome: resumed\nreleased: 3\n"},
		{"resume, dry run", []string{"resume", "--dry-run"}, pause(application.PauseResult{Watch: "github.com/o/x", Released: 3}), "outcome: would resume\nreleased: 3\n"},
		{"resume, not paused", []string{"resume"}, pause(application.PauseResult{Watch: "github.com/o/x", Already: true}), "outcome: not paused (no-op)\n"},
		{"interval", []string{"interval", "1m"}, interval(false), set + "outcome: set\n"},
		{"interval, dry run", []string{"interval", "1m", "--dry-run"}, interval(false), set + "outcome: would set\n"},
		{"interval, already", []string{"interval", "1m"}, interval(true), set + "outcome: already so (no-op)\n"},
		{"interval, already, dry run", []string{"interval", "1m", "--dry-run"}, interval(true), set + "outcome: already so (no-op)\n"},
	} {
		w := newFakeWorld()
		c.setup(w)
		code, out, errOut := w.run(t, c.args...)
		if code != exitOK || errOut != "" || !strings.Contains(out, c.want) || strings.Contains(out, "dryRun") {
			t.Errorf("%s: exit %d stderr %q\n%s", c.name, code, errOut, out)
		}
	}
	// Under --json interval keeps its keys, and "already" appears only when true.
	for _, already := range []bool{false, true} {
		w := newFakeWorld()
		interval(already)(w)
		_, out, _ := w.run(t, "interval", "1m", "--json")
		got := object(t, out)
		if _, ok := got["already"]; ok != already || got["watch"] != "github.com/o/x" || got["interval"] != "1m0s" || got["default"] != false {
			t.Errorf("already=%v: %s", already, out)
		}
	}
}

// The selector a command builds reaches the use case whole: the repository
// and identity as typed, the session from the environment, and the folder.
func TestTheSelectorReachesTheUseCase(t *testing.T) {
	w := newFakeWorld()
	w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
	for _, args := range [][]string{{"pause", "github.com/o/x", "--identity", "jdoe"}, {"resume", "github.com/o/x", "--identity", "jdoe"},
		{"unregister", "github.com/o/x", "--identity", "jdoe"}, {"interval", "github.com/o/x", "1m", "--identity", "jdoe"}} {
		if code, out, _ := w.run(t, args...); code != exitOK {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
	}
	want := application.Selector{Repo: "github.com/o/x", Identity: "jdoe", Session: "s1", Dir: "/repo"}
	if len(w.watch.selectors) != 4 {
		t.Fatalf("%d selectors", len(w.watch.selectors))
	}
	for i, got := range w.watch.selectors {
		if got != want {
			t.Errorf("selector %d: %+v, want %+v", i, got, want)
		}
	}
	// Without a repository the selector is the folder and the session alone.
	w = newFakeWorld()
	w.env["CLAUDE_CODE_SESSION_ID"] = "s9"
	w.run(t, "pause")
	if got := w.watch.selectors[0]; got != (application.Selector{Session: "s9", Dir: "/repo"}) {
		t.Errorf("%+v", got)
	}
}
