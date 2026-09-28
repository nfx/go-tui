// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestFormatAgo(t *testing.T) {
	now := time.Date(2024, time.February, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		delta  time.Duration
		expect string
	}{
		{name: "now", delta: 0, expect: "now"},
		{name: "seconds", delta: 30 * time.Second, expect: "30sec ago"},
		{name: "minutes", delta: 5 * time.Minute, expect: "5min ago"},
		{name: "fortyFiveMinutes", delta: 45 * time.Minute, expect: "45min ago"},
		{name: "sixtyTwoMinutes", delta: 62 * time.Minute, expect: "1hr ago"},
		{name: "oneHundredFiveMinutes", delta: 105 * time.Minute, expect: "2hr ago"},
		{name: "hoursToDayLow", delta: 30 * time.Hour, expect: "1d ago"},
		{name: "hoursToDayHigh", delta: 47 * time.Hour, expect: "2d ago"},
		{name: "days", delta: 4 * 24 * time.Hour, expect: "4d ago"},
		{name: "weeks", delta: 3 * 7 * 24 * time.Hour, expect: "3wk ago"},
		{name: "months", delta: 4 * 30 * 24 * time.Hour, expect: "4mo ago"},
		{name: "roundDownYear", delta: 13 * 30 * 24 * time.Hour, expect: "1yr ago"},
		{name: "roundUpYear", delta: 22 * 30 * 24 * time.Hour, expect: "2yr ago"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, formatAgo(now, now.Add(-tc.delta)))
		})
	}
}
