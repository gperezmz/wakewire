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
