package application_test

import (
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// binary is a service as one run of the wakewire binary builds it: it has
// its own process, whichever session's tick it answers.
func (h *harness) binary(pid int, start string) *application.Service {
	h.t.Helper()
	h.procs.Start(pid, start)
	repo, _ := domain.ParseRepo("github.com/o/x")
	return application.New(h.store, &fakes.Sources{Source: h.source}, &fakes.Accounts{
		Forge: application.AccountIdentity{Account: "gh", Service: "GitHub", Identity: "jdoe"},
	}, &fakes.Repos{Repo: repo, Files: map[string]string{"AGENTS.md": agents}}, h.procs, h.clock,
		application.Settings{Template: domain.DefaultTemplate()}, application.WithSelf(application.Process{PID: pid, Start: start}))
}

// Two ticks of one session share the session's process. The lease is held
// by the binary, so the second one does not poll while the first does.
func TestTwoTicksOfOneSessionDoNotPollTogether(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	first, second := h.binary(501, "b501"), h.binary(502, "b502")
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	if _, err := first.Next(h.ctx, "s1", 100, domain.ReaderTool); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(3 * time.Minute)
	block := make(chan struct{})
	h.source.Blocker = block
	h.observe(h.clock.Now(), domain.Observation{Item: issue("ready"), At: h.clock.Now()})
	polling := h.source.PollCount()
	done := make(chan application.NextResult)
	go func() { r, _ := first.Next(h.ctx, "s1", 100, domain.ReaderTool); done <- r }()
	for h.source.PollCount() == polling {
		time.Sleep(time.Millisecond)
	}
	r, err := second.Next(h.ctx, "s1", 100, domain.ReaderTool)
	if err != nil || r.Polled || h.source.PollCount() != polling+1 {
		t.Fatalf("a second tick of the same session polled too: %+v %v (%d polls)", r, err, h.source.PollCount()-polling)
	}
	close(block)
	if got := <-done; !got.Polled {
		t.Fatalf("%+v", got)
	}
}

// plantLease makes the binary holder own the watch's lease until the given
// time, as a tick of another session's binary that is polling now would.
func (h *harness) plantLease(holder application.Process, until time.Time) {
	h.t.Helper()
	err := h.store.Tx(h.ctx, func(tx application.Tx) error {
		ws, err := tx.Watches()
		if err != nil || len(ws) != 1 {
			return err
		}
		ok, err := tx.TakeLease(ws[0].ID, holder, until, func(application.Lease) bool { return true })
		if !ok {
			h.t.Fatal("the lease was not planted")
		}
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// A lease is free when its expiry passed, or when its process is gone, and
// a process id that a later program reuses is not the process that held it.
func TestALeaseIsFreeOnceItExpiresItsProcessIsGoneOrItsIDIsReused(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(h *harness) application.Process // the lease holder, planted for 90 s
		after time.Duration                        // how long after the lease was planted the tick runs
		polls bool
	}{
		{"held by a live process before its expiry", func(h *harness) application.Process {
			h.procs.Start(600, "b600")
			return application.Process{PID: 600, Start: "b600"}
		}, 30 * time.Second, false},
		{"held by a live process past its expiry", func(h *harness) application.Process {
			h.procs.Start(600, "b600")
			return application.Process{PID: 600, Start: "b600"}
		}, 91 * time.Second, true},
		{"held by a process that is gone", func(h *harness) application.Process {
			return application.Process{PID: 601, Start: "b601"}
		}, 30 * time.Second, true},
		{"held by a process whose id a later program reuses", func(h *harness) application.Process {
			h.procs.Start(602, "reused-later")
			return application.Process{PID: 602, Start: "the-holder"}
		}, 30 * time.Second, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.mustRegister("s1", 100)
			h.observe(t0, domain.Observation{Item: issue(), At: t0})
			h.next("s1", 100)
			h.clock.Advance(3 * time.Minute) // a poll is due
			h.plantLease(c.setup(h), h.clock.Now().Add(domain.LeaseFor))
			h.clock.Advance(c.after)
			h.observe(h.clock.Now(), domain.Observation{Item: issue(), At: h.clock.Now()})
			before := h.source.PollCount()
			n := h.next("s1", 100)
			if polled := h.source.PollCount() > before; polled != c.polls || n.Polled != c.polls {
				t.Fatalf("polled %v (result %v), want %v", polled, n.Polled, c.polls)
			}
		})
	}
}
