package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

const agents = "## Forge and tracker\n\nTracker: github-issues\n\n| Meaning | Label |\n|---|---|\n| spec | spec |\n| ready | ready |\n| in progress | in-progress |\n| needs input | needs-input |\n"

var t0 = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

type harness struct {
	t      *testing.T
	store  *fakes.Store
	clock  *fakes.Clock
	procs  *fakes.Processes
	source *fakes.Source
	svc    *application.Service
	ctx    context.Context
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, store: fakes.NewStore(), clock: &fakes.Clock{T: t0},
		procs: &fakes.Processes{Running: map[int]string{100: "s100", 200: "s200"}}, source: &fakes.Source{Name: "GitHub"}, ctx: context.Background()}
	repo, _ := domain.ParseRepo("github.com/o/x")
	h.svc = application.New(h.store, &fakes.Sources{Source: h.source}, &fakes.Accounts{
		Forge: application.AccountIdentity{Account: "gh", Service: "GitHub", Identity: "jdoe"},
	}, &fakes.Repos{Repo: repo, Files: map[string]string{"AGENTS.md": agents}}, h.procs, h.clock,
		application.Settings{Template: domain.DefaultTemplate()})
	return h
}

func (h *harness) register(session string, pid int, takeOver bool) (application.RegisterResult, error) {
	return h.svc.Register(h.ctx, application.RegisterInput{Session: session, Process: pid, TakeOver: takeOver})
}

func (h *harness) mustRegister(session string, pid int) application.RegisterResult {
	h.t.Helper()
	r, err := h.register(session, pid, false)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *harness) next(session string, pid int) application.NextResult {
	h.t.Helper()
	r, err := h.svc.Next(h.ctx, session, pid)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func code(err error) string {
	var c interface{ Code() string }
	if errors.As(err, &c) {
		return c.Code()
	}
	return ""
}

func issue(labels ...string) domain.Item {
	return domain.Item{Ref: "#12", Kind: domain.ItemIssue, URL: "u", State: "open", Labels: append([]string{"spec"}, labels...),
		Assignees: []string{"jdoe"}, Author: "alice"}
}

// observe scripts the source's next poll to show items.
func (h *harness) observe(at time.Time, items ...domain.Observation) {
	h.source.Script = append(h.source.Script, func(application.PollInput) (application.PollOutput, error) {
		return application.PollOutput{Observations: items, At: at}, nil
	})
}

func (h *harness) fail(code, msg string) {
	h.source.Script = append(h.source.Script, func(application.PollInput) (application.PollOutput, error) {
		return application.PollOutput{}, domain.Refuse(code, msg, "fix")
	})
}

func TestRegisterThenRefreshThenRefuseAnotherLiveSession(t *testing.T) {
	h := newHarness(t)
	if r := h.mustRegister("s1", 100); r.Outcome != "registered" || r.Tracker != "github-issues" || r.Identity != "jdoe" {
		t.Fatalf("%+v", r)
	}
	if r := h.mustRegister("s1", 100); r.Outcome != "refreshed" {
		t.Fatalf("%+v", r)
	}
	_, err := h.register("s2", 200, false)
	if code(err) != "held" || !strings.Contains(err.Error(), "s1") {
		t.Fatalf("%v", err)
	}
	if r, err := h.register("s2", 200, true); err != nil || r.Outcome != "taken-over" || r.Replaced != "s1" {
		t.Fatalf("%+v %v", r, err)
	}
	if n := h.next("s1", 100); n.State != "taken-over" || n.By != "claude-code:s2" {
		t.Fatalf("%+v", n)
	}
}

func TestRegisterNamesWhatTheSourceLacks(t *testing.T) {
	h := newHarness(t)
	repo, _ := domain.ParseRepo("github.com/o/x")
	for files, want := range map[string]string{
		"":                                  "no_source",
		"## Forge and tracker\n\nnothing\n": "source_incomplete",
	} {
		svc := application.New(h.store, &fakes.Sources{Source: h.source}, &fakes.Accounts{Forge: application.AccountIdentity{Service: "GitHub", Identity: "g"}},
			&fakes.Repos{Repo: repo, Files: map[string]string{"AGENTS.md": files}}, h.procs, h.clock, application.Settings{Template: domain.DefaultTemplate()})
		_, err := svc.Register(h.ctx, application.RegisterInput{Session: "s", Process: 100})
		if code(err) != want {
			t.Errorf("%q: %v", files, err)
		}
	}
	_, err := h.svc.Register(h.ctx, application.RegisterInput{Process: 100})
	if code(err) != "no_session" {
		t.Fatal(err)
	}
}

func TestDryRunRegistersNothing(t *testing.T) {
	h := newHarness(t)
	r, err := h.svc.Register(h.ctx, application.RegisterInput{Session: "s1", Process: 100, DryRun: true})
	if err != nil || r.Outcome != "registered" || r.Source.File != "AGENTS.md" || len(r.Source.Values) != 5 {
		t.Fatalf("%+v %v", r, err)
	}
	if st, _ := h.svc.Status(h.ctx, nil); len(st) != 0 {
		t.Fatal("dry run wrote a watch")
	}
}

func TestAChangeIsOfferedUntilAckedThenDelivered(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	if n := h.next("s1", 100); n.State != "armed" || !n.Polled || n.Batch != nil {
		t.Fatalf("first poll: %+v", n)
	}
	h.clock.Advance(3 * time.Minute)
	h.observe(t0.Add(3*time.Minute), domain.Observation{Item: issue("ready"), At: t0.Add(3 * time.Minute), Activities: []domain.Activity{
		{ID: "e1", Kind: domain.KindLabelAdded, Label: "ready", By: "alice", At: t0.Add(2 * time.Minute)},
	}})
	n := h.next("s1", 100)
	if n.Batch == nil || !strings.Contains(n.Batch.Wake, `#12: label "ready" added by alice`) {
		t.Fatalf("%+v", n)
	}
	h.clock.Advance(30 * time.Second)
	again := h.next("s1", 100) // not acked: the session quit before acking
	if again.Batch == nil || again.Batch.ID != n.Batch.ID || again.Polled {
		t.Fatalf("not offered again: %+v", again)
	}
	if a, err := h.svc.Ack(h.ctx, n.Batch.ID); err != nil || a.Delivered != 1 {
		t.Fatalf("%+v %v", a, err)
	}
	if a, _ := h.svc.Ack(h.ctx, n.Batch.ID); !a.Already {
		t.Fatal("second ack not idempotent")
	}
	h.clock.Advance(30 * time.Second)
	if n := h.next("s1", 100); n.Batch != nil {
		t.Fatalf("delivered batch offered again: %+v", n)
	}
	view, err := h.svc.Changes(h.ctx, n.Batch.ID)
	if err != nil || view.Protocol != 1 || len(view.Changes) != 1 || view.Changes[0].Label != "ready" {
		t.Fatalf("%+v %v", view, err)
	}
}

func TestACrashBeforeTheCommitLosesNothing(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	h.next("s1", 100)
	h.clock.Advance(3 * time.Minute)
	moved := domain.Observation{Item: issue("ready"), At: h.clock.Now()}
	h.observe(h.clock.Now(), moved)
	h.store.FailCommit = true // the poll's write never commits
	if _, err := h.svc.Next(h.ctx, "s1", 100); err == nil {
		t.Fatal("expected the crash")
	}
	h.clock.Advance(3 * time.Minute)
	h.observe(h.clock.Now(), moved) // the service still shows the label
	n := h.next("s1", 100)
	if n.Batch == nil || n.Batch.Count != 1 {
		t.Fatalf("the change was lost: %+v", n)
	}
}

func TestPauseHoldsAndResumeDeliversOneWake(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	h.next("s1", 100)
	sel := application.Selector{Repo: "github.com/o/x"}
	if _, err := h.svc.Pause(h.ctx, sel, 0, false); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(3 * time.Minute)
	h.observe(h.clock.Now(), domain.Observation{Item: issue("ready"), At: h.clock.Now()})
	n := h.next("s1", 100)
	if n.Batch != nil || n.Status.Held != 1 || n.Status.PausedSince == nil {
		t.Fatalf("%+v", n)
	}
	h.clock.Advance(3 * time.Minute)
	h.observe(h.clock.Now(), domain.Observation{Item: issue("ready"), At: h.clock.Now(), Activities: []domain.Activity{
		{ID: "c1", Kind: domain.KindComment, By: "bob", At: h.clock.Now(), Body: "go"},
	}})
	h.next("s1", 100)
	r, err := h.svc.Resume(h.ctx, sel, false)
	if err != nil || r.Released != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	h.clock.Advance(30 * time.Second)
	if n := h.next("s1", 100); n.Batch == nil || n.Batch.Count != 2 {
		t.Fatalf("%+v", n)
	}
}

func TestPauseForEndsOnItsOwn(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	sel := application.Selector{Repo: "github.com/o/x"}
	if _, err := h.svc.Pause(h.ctx, sel, 10*time.Minute, false); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(11 * time.Minute)
	if n := h.next("s1", 100); n.Status.PausedSince != nil {
		t.Fatalf("still paused: %+v", n.Status)
	}
}

func TestAProblemIsDeliveredOnceAndClears(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.fail("token_refused", "GitHub refused the token (401)")
	n := h.next("s1", 100)
	if n.Batch == nil || !strings.Contains(n.Batch.Wake, "not watching GitHub. GitHub refused the token (401).") {
		t.Fatalf("%+v", n.Batch)
	}
	if _, err := h.svc.Ack(h.ctx, n.Batch.ID); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(3 * time.Minute)
	h.fail("token_refused", "GitHub refused the token (401)")
	if n := h.next("s1", 100); n.Batch != nil || len(n.Status.Problems) != 1 {
		t.Fatalf("raised twice: %+v", n)
	}
	h.clock.Advance(3 * time.Minute)
	h.observe(h.clock.Now(), domain.Observation{Item: issue(), At: h.clock.Now()})
	if n := h.next("s1", 100); len(n.Status.Problems) != 0 {
		t.Fatalf("not cleared: %+v", n.Status)
	}
}

func TestUnreachableIsAProblemOnlyAfterThreePolls(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	for i := 1; i <= 3; i++ {
		h.fail("unreachable", "GitHub did not answer GET /x")
		n := h.next("s1", 100)
		if got := len(n.Status.Problems); (i < 3 && got != 0) || (i == 3 && got != 1) {
			t.Fatalf("poll %d: %d problems", i, got)
		}
		h.clock.Advance(3 * time.Minute)
	}
}

func TestKillNineMakesTheRegistrationDormantAndTakeoverInheritsWhatWaits(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	h.next("s1", 100)
	h.clock.Advance(3 * time.Minute)
	h.observe(h.clock.Now(), domain.Observation{Item: issue("ready"), At: h.clock.Now()})
	n := h.next("s1", 100) // offered, never acked
	h.procs.Kill(100)
	st, _ := h.svc.Status(h.ctx, nil)
	if st[0].Holder.State != "dormant" || st[0].Holder.Why != "its process is gone" {
		t.Fatalf("%+v", st[0].Holder)
	}
	if r := h.mustRegister("s2", 200); r.Outcome != "taken-over" {
		t.Fatalf("%+v", r)
	}
	h.clock.Advance(30 * time.Second)
	got := h.next("s2", 200)
	if got.Batch == nil || got.Batch.ID != n.Batch.ID {
		t.Fatalf("the waiting batch was not inherited: %+v", got)
	}
}

func TestEndedAndClearedSessionsLetGo(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	if _, err := h.svc.Hello(h.ctx, "s1", 100); err != nil {
		t.Fatal(err)
	}
	if r, _ := h.svc.Ended(h.ctx, "s1", "clear"); !r.Dormant {
		t.Fatalf("%+v", r)
	}
	if n := h.next("s1", 100); n.State != "idle" {
		t.Fatalf("an ended session still armed: %+v", n)
	}
	// The same process, after /clear, reports a new session id: the old
	// registration is dormant even without the ended report.
	h2 := newHarness(t)
	h2.mustRegister("s1", 100)
	h2.clock.Advance(time.Second)
	if hello, _ := h2.svc.Hello(h2.ctx, "s1-after-clear", 100); hello.Holds {
		t.Fatal("the new session holds a watch it never registered")
	}
	st, _ := h2.svc.Status(h2.ctx, nil)
	if st[0].Holder.State != "dormant" || st[0].Holder.Why != "its session was cleared" {
		t.Fatalf("%+v", st[0].Holder)
	}
}

func TestSilentAdapterGoesDormantAfterThreeIntervals(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.clock.Advance(7 * time.Minute)
	st, _ := h.svc.Status(h.ctx, nil)
	if st[0].Holder.State != "dormant" {
		t.Fatalf("%+v", st[0].Holder)
	}
}

func TestALeaseHeldByALiveProcessSkipsThePollButStillOffers(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	h.next("s1", 100)
	h.clock.Advance(3 * time.Minute)
	block := make(chan struct{})
	h.source.Blocker = block
	h.observe(h.clock.Now(), domain.Observation{Item: issue("ready"), At: h.clock.Now()})
	h.procs.Start(300, "s300")
	done := make(chan application.NextResult)
	go func() { r, _ := h.svc.Next(h.ctx, "s1", 100); done <- r }()
	for h.source.PollCount() < 2 {
		time.Sleep(time.Millisecond)
	}
	// A second tick from another process while the first polls.
	second, err := h.svc.Next(h.ctx, "s1", 300)
	if err != nil || second.Polled {
		t.Fatalf("polled twice at once: %+v %v", second, err)
	}
	close(block)
	if first := <-done; !first.Polled || first.Batch == nil {
		t.Fatalf("%+v", first)
	}
}

func TestIntervalAndUnregister(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	sel := application.Selector{Repo: "github.com/o/x"}
	r, err := h.svc.SetInterval(h.ctx, sel, 45*time.Second, false)
	if err != nil || r.Interval != "45s" {
		t.Fatalf("%+v %v", r, err)
	}
	if u, err := h.svc.Unregister(h.ctx, sel, false); err != nil || !u.Removed {
		t.Fatalf("%+v %v", u, err)
	}
	if u, err := h.svc.Unregister(h.ctx, sel, false); err != nil || !u.Already {
		t.Fatalf("not safe to repeat: %+v %v", u, err)
	}
	if _, err := h.svc.Pause(h.ctx, sel, 0, false); code(err) != "not_registered" {
		t.Fatal(err)
	}
}
