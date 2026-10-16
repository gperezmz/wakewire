package domain

import "testing"

func TestNearlySpentIsBelowFivePercent(t *testing.T) {
	for _, c := range []struct {
		remaining, limit int
		want             bool
	}{{250, 5000, false}, {249, 5000, true}, {0, 5000, true}, {500, 5000, false}, {0, 0, false}, {3, 60, false}, {2, 60, true}} {
		if got := NearlySpent(c.remaining, c.limit); got != c.want {
			t.Errorf("%d of %d: %v", c.remaining, c.limit, got)
		}
	}
}
