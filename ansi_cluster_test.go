// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestClusters(t *testing.T) {
	got := Text("ae\u0301\u754c\x1b[0m👍🏽").Clusters()
	assert.Equal(t, Clusters{
		{0, 1, 1}, {1, 3, 1}, {3, 4, 2}, {4, 8, 0}, {8, 10, 2},
	}, got)
}
