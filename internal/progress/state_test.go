// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package progress

import (
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestProgressStateIsDone(t *testing.T) {
	p := &progressState{maxNum: 0, currentNum: 0}
	if p.isDone() {
		t.Fatalf("expected not done")
	}
	p.maxNum = 2
	p.currentNum = 2
	if !p.isDone() {
		t.Fatalf("expected done")
	}
}

func TestProgressStateIncrementCalculatesElapsedFromNow(t *testing.T) {
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	p := &progressState{
		maxNum:     10,
		currentNum: 5,
		startedAt:  start,
		redrawAt:   start.Add(time.Second),
	}
	now := start.Add(3 * time.Second)
	p.increment(now)
	assert.Equal(t, 3*time.Second, p.elapsed)
	assert.Equal(t, int64(3), p.metricsSnapshot().Elapsed)
}
