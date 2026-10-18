package cli

import (
	"testing"
	"time"
)

// A duration takes days as well as Go's units, and interval refuses one
// shorter than 30s, or none at all, as an invalid value.
func TestDurationsTakeDaysAndIntervalRefusesWhatIsTooShort(t *testing.T) {
	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "90s": 90 * time.Second, "default": 0} {
		w := newFakeWorld()
		if code, out, _ := w.run(t, "interval", in, "--json"); code != 0 {
			t.Fatalf("%s: exit %d\n%s", in, code, out)
		}
		if got := w.watch.intervals; len(got) != 1 || got[0] != want {
			t.Errorf("%s: %v", in, got)
		}
	}
	for _, in := range []string{"29s", "soon", "0d"} {
		w := newFakeWorld()
		code, out, _ := w.run(t, "interval", in, "--json")
		if code != exitUsage || errorCode(t, out) != "invalid_value" || len(w.watch.intervals) != 0 {
			t.Errorf("%s: exit %d\n%s", in, code, out)
		}
	}
	w := newFakeWorld()
	if code, out, _ := w.run(t, "pause", "--for", "2d", "--json"); code != 0 || w.watch.pausedFor[0] != 48*time.Hour {
		t.Fatalf("pause --for 2d: exit %d %v\n%s", code, w.watch.pausedFor, out)
	}
}
