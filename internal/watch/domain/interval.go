package domain

import (
	"fmt"
	"math"
	"strconv"
	"strings"
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

// ParseDuration reads a positive duration: a Go duration such as 90s, 30m
// or 2h, or a number of days such as 7d. The CLI's rule for every duration
// it takes, cli.md; false when s is neither.
func ParseDuration(s string) (time.Duration, bool) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err == nil && days > 0 && days <= math.MaxInt64/int(24*time.Hour) {
			return time.Duration(days) * 24 * time.Hour, true
		}
		return 0, false
	}
	d, err := time.ParseDuration(s)
	return d, err == nil && d > 0
}

// ParseInterval reads a poll interval of at least 30 s.
func ParseInterval(s string) (time.Duration, error) {
	d, ok := ParseDuration(s)
	if !ok || d < MinInterval {
		return 0, Invalid(fmt.Sprintf("interval %q is not a duration of 30s or more", s),
			"use a duration such as 30s, 2m, 1h or 1d, or `default`")
	}
	return d, nil
}
