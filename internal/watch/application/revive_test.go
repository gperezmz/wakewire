package application_test

import (
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// registerElsewhere registers the session for another repository, over the
// same state, which ends its registration for the first.
func (h *harness) registerElsewhere(session string, pid int) *application.Service {
	h.t.Helper()
	repo, _ := domain.ParseRepo("github.com/o/y")
	svc := application.New(h.store, &fakes.Sources{Source: h.source}, &fakes.Accounts{
		Forge: application.AccountIdentity{Account: "gh", Service: "GitHub", Identity: "jdoe"},
	}, &fakes.Repos{Repo: repo, Files: map[string]string{"AGENTS.md": agents}}, h.procs, h.clock,
		application.Settings{Template: domain.DefaultTemplate()})
	if _, err := svc.Register(h.ctx, application.RegisterInput{Session: session, Process: pid}); err != nil {
		h.t.Fatal(err)
	}
	return svc
}

func TestARegistrationEndedForAnotherWatchDoesNotComeBack(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.clock.Advance(time.Minute)
	other := h.registerElsewhere("s1", 100) // ends the registration for o/x
	h.clock.Advance(time.Minute)
	if _, err := other.Unregister(h.ctx, application.Selector{Repo: "github.com/o/y"}, false); err != nil {
		t.Fatal(err)
	}
	// The session restarts in a new process; with the second watch gone,
	// the first registration is the one it finds for its id.
	h.procs.Kill(100)
	h.procs.Start(150, "s150")
	hello, err := h.svc.Hello(h.ctx, "s1", 150, domain.Protocol)
	if err != nil || hello.Holds {
		t.Fatalf("a registration ended on purpose came back: %+v %v", hello, err)
	}
}

func TestOnlyASessionOrProcessThatWentAwayComesBack(t *testing.T) {
	for reason, back := range map[string]bool{
		"prompt_input_exit": true, "other": true, "logout": true, "resume": true,
		"clear": false, "registered another watch": false, "": false,
	} {
		h := newHarness(t)
		h.mustRegister("s1", 100)
		if _, err := h.svc.Ended(h.ctx, "s1", reason); err != nil {
			t.Fatal(err)
		}
		h.procs.Kill(100)
		h.procs.Start(150, "s150")
		hello, err := h.svc.Hello(h.ctx, "s1", 150, domain.Protocol)
		if err != nil || hello.Holds != back {
			t.Errorf("ended %q: holds %v, want %v (%v)", reason, hello.Holds, back, err)
		}
	}
}

func TestASessionWhoseProcessWasKilledComesBackButNotWhileItStillRuns(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	holder := func() int {
		st, err := h.svc.Status(h.ctx, nil)
		if err != nil || len(st) != 1 || st[0].Holder == nil {
			t.Fatalf("%+v %v", st, err)
		}
		return st[0].Holder.PID
	}
	h.procs.Start(150, "s150")
	if _, err := h.svc.Hello(h.ctx, "s1", 150, domain.Protocol); err != nil || holder() != 100 {
		t.Fatalf("the registering process still runs, yet the registration moved to pid %d (%v)", holder(), err)
	}
	h.procs.Kill(100) // kill -9: no ended report
	if _, err := h.svc.Hello(h.ctx, "s1", 150, domain.Protocol); err != nil || holder() != 150 {
		t.Fatalf("a session whose process was killed did not come back: pid %d (%v)", holder(), err)
	}
}
