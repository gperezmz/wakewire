package application_test

import (
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/watch/application"
)

// --dry-run changes nothing: not even the daily clean-up a register would
// otherwise run, which records when it ran.
func TestARegisterDryRunDoesNotRunTheDailyCleanUp(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Register(h.ctx, application.RegisterInput{Session: "s1", Process: 100, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	err := h.store.Tx(h.ctx, func(tx application.Tx) error {
		at, err := tx.PrunedAt()
		if !at.IsZero() {
			t.Errorf("a dry run recorded a clean-up at %v", at)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h.mustRegister("s1", 100) // a real register still does
	err = h.store.Tx(h.ctx, func(tx application.Tx) error {
		at, err := tx.PrunedAt()
		if at.IsZero() {
			t.Error("a register did not run the clean-up")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
