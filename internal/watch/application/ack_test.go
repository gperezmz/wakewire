package application_test

import (
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
)

func (h *harness) problems() map[string]application.Problem {
	h.t.Helper()
	out := map[string]application.Problem{}
	err := h.store.Tx(h.ctx, func(tx application.Tx) error {
		all, err := tx.Watches()
		if err != nil || len(all) == 0 {
			return err
		}
		ps, err := tx.OpenProblems(all[0].ID)
		for _, p := range ps {
			out[p.Code] = p
		}
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

// A problem is delivered once, as a change of its own in a batch. Acking
// that batch must not mark a problem raised after it was offered.
func TestAckDeliversOnlyTheProblemsItsBatchReported(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	h.fail("token_refused", "GitHub refused the token (401)")
	first := h.next("s1", 100)
	if first.Batch == nil {
		t.Fatalf("%+v", first)
	}
	h.clock.Advance(3 * time.Minute)
	h.fail("rate_limited", "GitHub's rate limit is spent")
	second := h.next("s1", 100) // a second problem: a new batch takes the first one's place
	if second.Batch == nil || second.Batch.ID == first.Batch.ID {
		t.Fatalf("%+v", second)
	}
	if _, err := h.svc.Ack(h.ctx, first.Batch.ID); err != nil {
		t.Fatal(err)
	}
	ps := h.problems()
	if ps["token_refused"].DeliveredAt == nil {
		t.Fatal("the problem the batch reported was not marked delivered")
	}
	if ps["rate_limited"].DeliveredAt != nil {
		t.Fatal("a problem the batch did not report was marked delivered")
	}
	if _, err := h.svc.Ack(h.ctx, second.Batch.ID); err != nil {
		t.Fatal(err)
	}
	if h.problems()["rate_limited"].DeliveredAt == nil {
		t.Fatal("the second batch's ack did not deliver its problem")
	}
}
