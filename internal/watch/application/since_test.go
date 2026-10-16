package application_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// A poll takes time. What happens on the service while it reads may be
// missed by that poll, and an item it shows the next time as new must
// report what happened since that poll began.
func TestActivityDuringAPollIsReportedByTheNextOne(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	h.next("s1", 100)

	h.clock.Advance(3 * time.Minute)
	began := h.clock.Now()
	h.source.Script = append(h.source.Script, func(application.PollInput) (application.PollOutput, error) {
		h.clock.Advance(20 * time.Second) // a slow service: the poll ends 20 s after it began
		return application.PollOutput{At: h.clock.Now()}, nil
	})
	h.next("s1", 100)

	// A spec labelled 5 s into that poll, after the list was read.
	h.clock.Advance(3 * time.Minute)
	other := issue()
	other.Ref = "#13"
	h.observe(h.clock.Now(), domain.Observation{Item: other, At: h.clock.Now(), Activities: []domain.Activity{
		{ID: "e1", Kind: domain.KindLabelAdded, Label: "spec", By: "alice", At: began.Add(5 * time.Second)},
	}})
	n := h.next("s1", 100)
	if n.Batch == nil || !strings.Contains(n.Batch.Wake, `#13: label "spec" added by alice`) {
		t.Fatalf("the activity during the poll was lost: %+v", n)
	}
}

// A source that failed for a while is read back to its last good read, not
// to the watch's last poll.
func TestAnItemNewAfterAnOutageReportsWhatHappenedDuringIt(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.observe(t0, domain.Observation{Item: issue(), At: t0})
	h.next("s1", 100)
	for range 2 { // two polls fail: the watch is polled, the source is not read
		h.clock.Advance(3 * time.Minute)
		h.fail("unreachable", "GitHub did not answer")
		h.next("s1", 100)
	}
	h.clock.Advance(3 * time.Minute)
	other := issue()
	other.Ref = "#13"
	h.observe(h.clock.Now(), domain.Observation{Item: other, At: h.clock.Now(), Activities: []domain.Activity{
		{ID: "e1", Kind: domain.KindLabelAdded, Label: "spec", By: "alice", At: t0.Add(4 * time.Minute)},
	}})
	n := h.next("s1", 100)
	if n.Batch == nil || !strings.Contains(n.Batch.Wake, `#13: label "spec" added by alice`) {
		t.Fatalf("the activity during the outage was lost: %+v", n)
	}
}
