package domain

import (
	"testing"
	"time"
)

func TestDurationsTakeGoFormsAndDays(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"90s": 90 * time.Second, "30m": 30 * time.Minute, "2h": 2 * time.Hour, "1h30m": 90 * time.Minute,
		"7d": 7 * 24 * time.Hour, "1d": 24 * time.Hour, "30d": 30 * 24 * time.Hour,
	} {
		if got, ok := ParseDuration(in); !ok || got != want {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "d", "0d", "-1d", "1.5d", "7 d", "abc", "0s", "-5m", "9999999999d", "1w"} {
		if got, ok := ParseDuration(in); ok {
			t.Errorf("%q was read as %v", in, got)
		}
	}
}

func TestAnIntervalTakesDaysButNotLessThanThirtySeconds(t *testing.T) {
	if d, err := ParseInterval("1d"); err != nil || d != 24*time.Hour {
		t.Fatalf("%v %v", d, err)
	}
	for _, in := range []string{"29s", "0d", "default", "soon"} {
		if _, err := ParseInterval(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
	if d, err := ParseInterval("30s"); err != nil || d != MinInterval {
		t.Fatalf("%v %v", d, err)
	}
}
