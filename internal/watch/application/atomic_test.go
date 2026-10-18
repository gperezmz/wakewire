package application_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// tickWithLabelAdded runs the setup and the one tick in which a poll finds
// the ready label, and answers the store's commit numbers that tick spans.
func tickWithLabelAdded(t *testing.T, crashAt int) (first, last int, h *harness) {
	t.Helper()
	h = newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	h.next("s1", 100)
	h.clock.Advance(3 * time.Minute)
	h.observe(h.clock.Now(), domain.Observation{Item: issue("ready"), At: h.clock.Now()})
	first = h.store.Commits() + 1
	if crashAt > 0 {
		h.store.FailCommitNumber(first - 1 + crashAt)
	}
	_, _ = h.svc.Next(h.ctx, "s1", 100) // a crash returns an error
	return first, h.store.Commits(), h
}

// The poll writes a source's changes and its snapshot together. Whichever
// commit of the tick the process dies at, the label added is found exactly
// once afterwards: not lost with its snapshot moved on, not found twice
// with its snapshot left behind.
func TestWhereverATickCrashesTheChangeIsFoundOnce(t *testing.T) {
	first, last, _ := tickWithLabelAdded(t, 0)
	commits := last - first + 1
	if commits < 4 {
		t.Fatalf("a tick that polls spans %d commits: the test no longer covers the poll", commits)
	}
	for k := 1; k <= commits; k++ {
		_, _, h := tickWithLabelAdded(t, k)
		var wakes []string
		for range 3 {
			h.clock.Advance(3 * time.Minute)
			h.observe(h.clock.Now(), domain.Observation{Item: issue("ready"), At: h.clock.Now()}) // the service still shows it
			if n, err := h.svc.Next(h.ctx, "s1", 100); err != nil {
				t.Fatalf("commit %d crashed: the tick after it failed too: %v", k, err)
			} else if n.Batch != nil {
				wakes = append(wakes, n.Batch.Wake)
			}
		}
		led, err := h.svc.Ledger(h.ctx, application.Selector{Repo: "github.com/o/x"}, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, c := range led.Changes {
			if c.Kind == domain.KindLabelAdded && c.Label == "ready" {
				found++
			}
		}
		if found != 1 {
			t.Errorf("crash at commit %d of %d: the label added was recorded %d times", k, commits, found)
		}
		if !strings.Contains(strings.Join(wakes, "\n"), `#12: label "ready" added`) {
			t.Errorf("crash at commit %d of %d: no wake reported the label added: %q", k, commits, wakes)
		}
	}
}
