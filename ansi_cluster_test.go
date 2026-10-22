// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestClusters(t *testing.T) {
	got := text("ae\u0301\u754c\x1b[0m👍🏽").clusters()
	assert.Equal(t, clusters{
		{0, 1, 1}, {1, 3, 1}, {3, 4, 2}, {4, 8, 0}, {8, 10, 2},
	}, got)
}

// oneCluster reports whether line holds a single visible segment,
// which is wider than the viewport and cannot be wrapped.
func oneCluster(line text) bool {
	visible := 0
	for seg := range line.segments() {
		if seg.width > 0 {
			visible++
		}
	}
	return visible <= 1
}
