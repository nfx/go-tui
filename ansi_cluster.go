// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"unicode/utf8"
)

// cluster is a segment of text, measured in runes and columns.
type cluster struct {
	start, end int // rune offsets: [start, end)
	width      int
}

// clusters splits t into cursor and wrapping boundaries.
type clusters []cluster

func (t text) clusters() clusters {
	cells := make(clusters, 0, len(t))
	offset := 0
	for seg := range t.segments() {
		runes := utf8.RuneCount(seg.text)
		cells = append(cells, cluster{start: offset, end: offset + runes, width: seg.width})
		offset += runes
	}
	return cells
}

// width returns the terminal columns occupied by c.
func (c clusters) width() int {
	w := 0
	for _, cell := range c {
		w += cell.width
	}
	return w
}

// at returns the cluster containing cursor, or len(c) at the end.
func (c clusters) at(cursor int) int {
	for k, cell := range c {
		if cursor < cell.end {
			return k
		}
	}
	return len(c)
}

// runeOffset returns the rune offset at cluster k's start.
func (c clusters) runeOffset(k int) int {
	switch {
	case k < len(c):
		return c[k].start
	case len(c) > 0:
		return c[len(c)-1].end
	default:
		return 0
	}
}
