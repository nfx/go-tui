// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestDropdownClampDisplayShrink(t *testing.T) {
	d := &dropdown{
		relevant:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		displayed: []int{0, 1, 2, 3, 4},
		offset:    0,
		selected:  4,
	}
	// shrink to height 4 → capacity = 2
	d.clampDisplay(4)
	assert.Equal(t, 2, len(d.displayed))
	assert.True(t, d.selected < len(d.displayed))
}

func TestDropdownClampDisplayGrow(t *testing.T) {
	d := &dropdown{
		relevant:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		displayed: []int{0, 1},
		offset:    0,
		selected:  1,
	}
	// grow to height 20 → capacity = 10
	d.clampDisplay(20)
	assert.Equal(t, 10, len(d.displayed))
	assert.Equal(t, 1, d.selected)
}

func TestDropdownClampDisplayClampsOffset(t *testing.T) {
	d := &dropdown{
		relevant:  []int{0, 1, 2, 3, 4},
		displayed: []int{3, 4},
		offset:    3,
		selected:  1,
	}
	// height 6 → capacity = 3, offset 3 + 3 > 5 → clamp offset
	d.clampDisplay(6)
	assert.True(t, d.offset+len(d.displayed) <= len(d.relevant))
}

func TestMultichoiceClampRowsShrink(t *testing.T) {
	m := &multichoice{
		relevant:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		displayed: []int{0, 1, 2, 3, 4},
		offset:    0,
		active:    4,
	}
	m.clampRows(2)
	assert.Equal(t, 2, len(m.displayed))
	assert.True(t, m.active < len(m.displayed))
}
