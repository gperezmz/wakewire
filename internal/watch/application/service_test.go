package application_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
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
	if err != nil || view.Protocol != domain.Protocol || len(view.Changes) != 1 || view.Changes[0].Label != "ready" {
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
	if n.Batch == nil || !strings.Contains(n.Batch.Wake, "Not watching GitHub — GitHub refused the token (401).") {
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
	if _, err := h.svc.Hello(h.ctx, "s1", 100, domain.Protocol); err != nil {
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
	if hello, _ := h2.svc.Hello(h2.ctx, "s1-after-clear", 100, domain.Protocol); hello.Holds {
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

func TestAResumedSessionTakesItsRegistrationBack(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	if _, err := h.svc.Ended(h.ctx, "s1", "prompt_input_exit"); err != nil {
		t.Fatal(err)
	}
	h.procs.Kill(100)
	h.procs.Start(150, "s150") // claude --resume s1, in a new process
	h.clock.Advance(time.Minute)
	hello, err := h.svc.Hello(h.ctx, "s1", 150, domain.Protocol)
	if err != nil || !hello.Holds {
		t.Fatalf("%+v %v", hello, err)
	}
	st, _ := h.svc.Status(h.ctx, nil)
	if st[0].Holder.State != "held" || st[0].Holder.PID != 150 {
		t.Fatalf("%+v", st[0].Holder)
	}
}

func TestATakenOverOrClearedSessionDoesNotComeBack(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.procs.Kill(100)
	h.mustRegister("s2", 200)
	h.procs.Start(150, "s150")
	if hello, _ := h.svc.Hello(h.ctx, "s1", 150, domain.Protocol); hello.Holds {
		t.Fatal("a session taken over took the watch back")
	}
	h2 := newHarness(t)
	h2.mustRegister("s1", 100)
	if _, err := h2.svc.Ended(h2.ctx, "s1", "clear"); err != nil {
		t.Fatal(err)
	}
	if hello, _ := h2.svc.Hello(h2.ctx, "s1", 100, domain.Protocol); hello.Holds {
		t.Fatal("a cleared session took the watch back")
	}
}

func TestTheStatusLineSaysWhenTheLastChangeWasFound(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	if n := h.next("s1", 100); n.Status.LastChangeAt != nil {
		t.Fatalf("no change yet, but %v", n.Status.LastChangeAt)
	}
	h.clock.Advance(3 * time.Minute)
	found := h.clock.Now()
	h.observe(found, domain.Observation{Item: issue("ready", "in-progress"), At: found})
	n := h.next("s1", 100)
	if n.Status.LastChangeAt == nil || !n.Status.LastChangeAt.Equal(found) {
		t.Fatalf("%v", n.Status.LastChangeAt)
	}
}

// A batch that does not exist is its own refusal, for the reader and for the
// ack alike, so a caller can tell it from a malformed value.
func TestAckAndChangesOfAMissingBatchAreNoSuchBatch(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	if _, err := h.svc.Ack(h.ctx, 999); code(err) != "no_such_batch" {
		t.Errorf("ack: %v", err)
	}
	if _, err := h.svc.Changes(h.ctx, 999); code(err) != "no_such_batch" {
		t.Errorf("changes: %v", err)
	}
}

// Asking for the interval a watch has changes nothing and says so.
func TestSetIntervalSaysWhenItIsAlreadySo(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	sel := application.Selector{Repo: "github.com/o/x"}
	for _, c := range []struct {
		d       time.Duration
		dry     bool
		already bool
	}{{45 * time.Second, true, false}, {45 * time.Second, false, false}, {45 * time.Second, false, true},
		{45 * time.Second, true, true}, {2 * time.Minute, false, false}, {0, false, false}, {0, false, true}} {
		r, err := h.svc.SetInterval(h.ctx, sel, c.d, c.dry)
		if err != nil || r.Already != c.already {
			t.Errorf("%v dry=%v: already %v, want %v (%v)", c.d, c.dry, r.Already, c.already, err)
		}
	}
}

// A comment is stored and served whole, however long: nothing between the
// source and the reader cuts it, and the reader (wakewire changes) clips what
// it shows. A long echo is still ignored by its closing marker.
func TestALongCommentIsStoredWholeAndALongEchoIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	h.next("s1", 100)
	h.clock.Advance(3 * time.Minute)
	long := strings.Repeat("a long verdict\n", 3000) // 45000 characters
	echo := long + "\n---\n" + domain.DefaultMarker + "\n"
	item := issue()
	item.Comments = 2
	at := h.clock.Now()
	h.observe(at, domain.Observation{Item: item, At: at, Activities: []domain.Activity{
		{ID: "c1", Kind: domain.KindComment, By: "alice", At: at, Body: long},
		{ID: "c2", Kind: domain.KindComment, By: "jdoe", At: at, Body: echo},
	}})
	n := h.next("s1", 100)
	if n.Batch == nil || n.Batch.Count != 1 {
		t.Fatalf("only the long comment by someone else wakes the holder: %+v", n)
	}
	view, err := h.svc.Changes(h.ctx, n.Batch.ID)
	if err != nil || len(view.Changes) != 1 {
		t.Fatalf("%+v %v", view, err)
	}
	if c := view.Changes[0]; c.Body == nil || *c.Body != long || c.Truncated != nil {
		t.Fatalf("the body was not stored whole: %d chars, truncated %v", len([]rune(*c.Body)), c.Truncated)
	}
	led, err := h.svc.Ledger(h.ctx, application.Selector{Repo: "github.com/o/x"}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var ignored int
	for _, e := range led.Changes {
		if e.Status == domain.StatusIgnored && strings.HasPrefix(e.Reason, "echo") {
			ignored++
		}
	}
	if ignored != 1 {
		t.Fatalf("the long echo was not ignored: %+v", led.Changes)
	}
}

// An adapter that speaks another protocol is refused at hello, under a code
// of its own, whatever the binary would have answered; 0 is an adapter that
// does not say, which older callers and doctor are.
func TestHelloRefusesAnAdapterOfAnotherProtocol(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	for _, p := range []int{1, 3, -1} {
		if _, err := h.svc.Hello(h.ctx, "s1", 100, p); code(err) != "protocol_mismatch" {
			t.Errorf("protocol %d: %v", p, err)
		}
	}
	for _, p := range []int{0, domain.Protocol} {
		if r, err := h.svc.Hello(h.ctx, "s1", 100, p); err != nil || !r.Holds {
			t.Errorf("protocol %d: %+v %v", p, r, err)
		}
	}
	// A refused hello records nothing: no adapter is heard from.
	h2 := newHarness(t)
	h2.mustRegister("s1", 100)
	_, _ = h2.svc.Hello(h2.ctx, "s1", 100, 1)
	if a, _ := h2.svc.AdapterOf(h2.ctx, "s1"); a != nil {
		t.Errorf("a refused adapter was recorded: %+v", a)
	}
}

// Two identities watching one repository cannot be told apart by the
// repository alone: the refusal is its own code, and names the way out.
func TestSeveralIdentitiesOnOneRepositoryAreAmbiguousWithoutAnIdentity(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	repo, _ := domain.ParseRepo("github.com/o/x")
	other := application.New(h.store, &fakes.Sources{Source: h.source}, &fakes.Accounts{
		Forge: application.AccountIdentity{Account: "gh2", Service: "GitHub", Identity: "other"},
	}, &fakes.Repos{Repo: repo, Files: map[string]string{"AGENTS.md": agents}}, h.procs, h.clock,
		application.Settings{Template: domain.DefaultTemplate()})
	if _, err := other.Register(h.ctx, application.RegisterInput{Session: "s2", Process: 200}); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.Ledger(h.ctx, application.Selector{Repo: "github.com/o/x"}, 0, "")
	if code(err) != "ambiguous_watch" || !strings.Contains(err.Error(), "2 identities watch github.com/o/x") {
		t.Fatalf("%v", err)
	}
	var hinted interface{ Hint() string }
	if !errors.As(err, &hinted) || !strings.Contains(hinted.Hint(), "--identity") {
		t.Errorf("the hint does not name --identity: %v", err)
	}
	if _, err = h.svc.Ledger(h.ctx, application.Selector{Repo: "github.com/o/x", Identity: "other"}, 0, ""); err != nil {
		t.Errorf("with an identity: %v", err)
	}
}

// Failures counts unreachable polls in a row: another kind of failure between
// them starts the count again, so a flaky token or a spent budget never adds
// up to an outage.
func TestOnlyUnreachablePollsInARowRaiseUnreachable(t *testing.T) {
	problems := func(h *harness) int { return len(h.next("s1", 100).Status.Problems) }
	poll := func(h *harness, code string) int {
		h.fail(code, "failed")
		n := problems(h)
		h.clock.Advance(3 * time.Minute)
		return n
	}
	h := newHarness(t)
	h.mustRegister("s1", 100)
	// A rate limit is a problem of its own, at once; it must not count toward the three.
	poll(h, "rate_limited")
	for i, want := range []int{1, 1} { // rate_limited stays open; unreachable not yet raised
		if got := poll(h, "unreachable"); got != want {
			t.Fatalf("unreachable #%d after a rate limit: %d problems, want %d", i+1, got, want)
		}
	}
	for _, p := range statusProblems(t, h) {
		if p == "unreachable" {
			t.Fatalf("unreachable was raised after two polls that followed a rate limit: %v", statusProblems(t, h))
		}
	}
	// A third in a row raises it.
	poll(h, "unreachable")
	if got := statusProblems(t, h); !slices.Contains(got, "unreachable") {
		t.Fatalf("three unreachable polls in a row did not raise it: %v", got)
	}

	// Two, then another failure, then two more: still not raised.
	h = newHarness(t)
	h.mustRegister("s1", 100)
	for _, code := range []string{"unreachable", "unreachable", "token_refused", "unreachable", "unreachable"} {
		poll(h, code)
	}
	if got := statusProblems(t, h); slices.Contains(got, "unreachable") {
		t.Fatalf("a count that another failure should have reset raised unreachable: %v", got)
	}
}

// statusProblems names the problems a watch has open.
func statusProblems(t *testing.T, h *harness) []string {
	t.Helper()
	st, err := h.svc.Status(h.ctx, nil)
	if err != nil || len(st) != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	var codes []string
	for _, p := range st[0].Problems {
		codes = append(codes, p.Code)
	}
	return codes
}

// A pause already on is a no-op when asked again bare: it keeps its end. Asked
// again with --for it takes the new end, and is no longer "already".
func TestPausingAWatchAlreadyPausedKeepsOrMovesItsEnd(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	sel := application.Selector{Repo: "github.com/o/x"}
	first, err := h.svc.Pause(h.ctx, sel, time.Hour, false)
	if err != nil || first.Already || first.Until == nil || !first.Until.Equal(t0.Add(time.Hour)) {
		t.Fatalf("%+v %v", first, err)
	}
	h.clock.Advance(10 * time.Minute)

	again, err := h.svc.Pause(h.ctx, sel, 0, false)
	if err != nil || !again.Already {
		t.Fatalf("a bare second pause is not a no-op: %+v %v", again, err)
	}
	if again.Until == nil || !again.Until.Equal(t0.Add(time.Hour)) || again.PausedSince == nil || !again.PausedSince.Equal(t0) {
		t.Fatalf("the bare second pause changed the pause: %+v", again)
	}
	st, _ := h.svc.Status(h.ctx, nil)
	if st[0].PausedUntil == nil || !st[0].PausedUntil.Equal(t0.Add(time.Hour)) {
		t.Fatalf("the stored end moved: %v", st[0].PausedUntil)
	}

	moved, err := h.svc.Pause(h.ctx, sel, 2*time.Hour, false)
	if err != nil || moved.Already || moved.Until == nil || !moved.Until.Equal(t0.Add(10*time.Minute+2*time.Hour)) {
		t.Fatalf("--for did not set a new end: %+v %v", moved, err)
	}
	if !moved.PausedSince.Equal(t0) {
		t.Fatalf("the pause's start moved: %v", moved.PausedSince)
	}
	st, _ = h.svc.Status(h.ctx, nil)
	if st[0].PausedUntil == nil || !st[0].PausedUntil.Equal(t0.Add(10*time.Minute+2*time.Hour)) {
		t.Fatalf("the new end was not stored: %v", st[0].PausedUntil)
	}

	// A pause with no end, asked again bare, stays without one; --for gives it one.
	h2 := newHarness(t)
	h2.mustRegister("s1", 100)
	if r, _ := h2.svc.Pause(h2.ctx, sel, 0, false); r.Until != nil || r.Already {
		t.Fatalf("%+v", r)
	}
	if r, _ := h2.svc.Pause(h2.ctx, sel, 0, false); !r.Already || r.Until != nil {
		t.Fatalf("%+v", r)
	}
	if r, _ := h2.svc.Pause(h2.ctx, sel, time.Hour, false); r.Already || r.Until == nil {
		t.Fatalf("%+v", r)
	}
}

// bothHalves binds a forge that works and a tracker that cannot be read, as
// the real sources do when a Jira site has no account.
type bothHalves struct {
	forge      *fakes.Source
	trackerErr error
}

func (b bothHalves) For(_ context.Context, w application.Watch) ([]application.BoundSource, error) {
	rules := domain.Rules{Identity: w.Identity, Labels: w.Labels, Marker: w.Marker}
	return []application.BoundSource{
		{Name: application.SourceForge, Source: b.forge, Rules: rules, IncludeIssues: true},
		{Name: application.SourceTracker, Source: brokenSource{b.trackerErr}, Rules: rules},
	}, nil
}

type brokenSource struct{ err error }

func (brokenSource) Service() string { return "Jira" }
func (brokenSource) Account() string { return "" }
func (b brokenSource) Poll(context.Context, application.PollInput) (application.PollOutput, error) {
	return application.PollOutput{}, b.err
}

// One source that cannot be read fails alone: the other half polls, its
// changes are offered, and the problem is the broken half's own.
func TestAFailingTrackerDoesNotStopTheForge(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	svc := application.New(h.store, bothHalves{h.source, domain.Refuse("no_account", "no account covers the Jira site x.atlassian.net", "add one")},
		&fakes.Accounts{Forge: application.AccountIdentity{Account: "gh", Service: "GitHub", Identity: "jdoe"}},
		&fakes.Repos{Files: map[string]string{"AGENTS.md": agents}}, h.procs, h.clock, application.Settings{Template: domain.DefaultTemplate()})
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	if _, err := svc.Next(h.ctx, "s1", 100); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(3 * time.Minute)
	h.observe(h.clock.Now(), domain.Observation{Item: issue("ready"), At: h.clock.Now()})
	n, err := svc.Next(h.ctx, "s1", 100)
	if err != nil || n.Batch == nil || !strings.Contains(n.Batch.Wake, `label "ready" added`) {
		t.Fatalf("the forge's change was not offered: %+v %v", n, err)
	}
	st, err := svc.Status(h.ctx, nil)
	if err != nil || len(st) != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	var onTracker, onForge int
	for _, p := range st[0].Problems {
		if p.Source == application.SourceTracker && p.Code == "no_account" {
			onTracker++
		}
		if p.Source == application.SourceForge {
			onForge++
		}
	}
	if onTracker != 1 || onForge != 0 {
		t.Fatalf("problems %+v: want one on the tracker and none on the forge", st[0].Problems)
	}
}
