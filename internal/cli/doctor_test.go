package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
)

func statusWithRate(remaining, limit int) []application.WatchStatus {
	return []application.WatchStatus{{
		Watch: "github.com/o/x",
		Sources: []application.SourceStatus{{Name: application.SourceForge, Service: "GitHub",
			Rate: &application.RateInfo{Remaining: remaining, Limit: limit, ResetAt: time.Now().Add(time.Hour)}}},
	}}
}

// The budget doctor warns about is the one at which a poll stops reading
// the source: the same constant, not two.
func TestDoctorWarnsOfTheBudgetExactlyWhereAPollStopsReading(t *testing.T) {
	for _, c := range []struct {
		remaining int
		warns     bool
	}{{700, false}, {260, false}, {240, true}, {0, true}} {
		w := newFakeWorld()
		w.watch.statuses = statusWithRate(c.remaining, 5000)
		code, out, _ := w.run(t, "doctor")
		if code != 0 {
			t.Fatalf("%d left: exit %d\n%s", c.remaining, code, out)
		}
		if got := strings.Contains(out, "warn budget"); got != c.warns {
			t.Errorf("%d of 5000 left: warned %v\n%s", c.remaining, got, out)
		}
	}
}
