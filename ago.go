// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"math"
	"time"
)

const (
	dayDuration   = 24 * time.Hour
	weekDuration  = 7 * dayDuration
	monthDuration = 30 * dayDuration
	yearDuration  = 365 * dayDuration
)

// ago renders human-friendly relative times for templates.
func ago(t time.Time) string {
	return formatAgo(time.Now(), t)
}

func formatAgo(now, t time.Time) string {
	duration := now.Sub(t)
	future := duration < 0
	if future {
		duration = -duration
	}
	if duration < time.Second {
		return "now"
	}

	type unit struct {
		limit  time.Duration
		base   time.Duration
		suffix string
	}

	units := []unit{
		{limit: time.Minute, base: time.Second, suffix: "sec"},
		{limit: time.Hour, base: time.Minute, suffix: "min"},
		{limit: dayDuration, base: time.Hour, suffix: "hr"},
		{limit: weekDuration, base: dayDuration, suffix: "d"},
		{limit: monthDuration, base: weekDuration, suffix: "wk"},
		{limit: yearDuration, base: monthDuration, suffix: "mo"},
	}

	format := func(value float64, suffix string) string {
		rounded := int(math.Round(value))
		if future {
			return fmt.Sprintf("in %d%s", rounded, suffix)
		}
		return fmt.Sprintf("%d%s ago", rounded, suffix)
	}

	for _, u := range units {
		if duration >= u.limit {
			continue
		}
		value := float64(duration) / float64(u.base)
		if math.Round(value) >= float64(u.limit)/float64(u.base) {
			continue
		}
		return format(value, u.suffix)
	}
	return format(float64(duration)/float64(yearDuration), "yr")
}
