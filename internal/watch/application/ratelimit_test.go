package application_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// limited is a service's rate-limit failure carrying its reset time, as
// httpread's error does.
type limited struct{ reset time.Time }

func (limited) Error() string      { return "GitHub's rate limit is spent" }
func (limited) Code() string       { return "rate_limited" }
func (limited) Hint() string       { return "wait for the reset" }
func (l limited) Reset() time.Time { return l.reset }

func TestARateLimitedSourceIsNotReadUntilItsReset(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	h.next("s1", 100)
	h.clock.Advance(3 * time.Minute)
	reset := h.clock.Now().Add(20 * time.Minute)
	h.source.Script = append(h.source.Script, func(application.PollInput) (application.PollOutput, error) {
		return application.PollOutput{}, fmt.Errorf("listing: %w", limited{reset})
	})
	n := h.next("s1", 100)
	if len(n.Status.Problems) != 1 || n.Status.Problems[0].Code != "rate_limited" {
		t.Fatalf("%+v", n.Status)
	}
	polls := h.source.PollCount()
	for range 4 { // ticks and intervals pass, the reset does not
		h.clock.Advance(4 * time.Minute)
		h.next("s1", 100)
	}
	if got := h.source.PollCount(); got != polls {
		t.Fatalf("the source was read %d more time(s) before its reset", got-polls)
	}
	h.clock.Advance(5 * time.Minute) // past the reset
	h.observe(h.clock.Now(), domain.Observation{Item: issue(), At: h.clock.Now()})
	if n := h.next("s1", 100); !n.Polled || len(n.Status.Problems) != 0 || h.source.PollCount() != polls+1 {
		t.Fatalf("%+v", n)
	}
}
