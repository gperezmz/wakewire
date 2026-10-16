package domain

import (
	"fmt"
	"time"
)

// Fixed in v1; see configuration.md.
const (
	Tick            = 30 * time.Second
	MinInterval     = 30 * time.Second
	DefaultInterval = 2 * time.Minute
	PollBudget      = 60 * time.Second
	LeaseFor        = 90 * time.Second
	DormantAfter    = 3 // intervals without the adapter heard from
	KeepFor         = 30 * 24 * time.Hour
	MaxWakeLines    = 20
	BodyLimit       = 4000
	UnreachableAt   = 3 // polls in a row
)

// ParseInterval reads a poll interval of at least 30 s.
func ParseInterval(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d < MinInterval {
		return 0, Invalid(fmt.Sprintf("interval %q is not a duration of 30s or more", s),
			"use a duration such as 30s, 2m or 1h, or `default`")
	}
	return d, nil
}
