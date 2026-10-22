// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package ansi

import (
	"unicode/utf8"
)

// cluster is a segment of text, measured in runes and columns.
type cluster struct {
	Start, End int // rune offsets: [start, end)
	Width      int
}

// Clusters splits t into cursor and wrapping boundaries.
type Clusters []cluster

func (t Text) Clusters() Clusters {
	cells := make(Clusters, 0, len(t))
	offset := 0
	for seg := range t.Segments() {
		runes := utf8.RuneCount(seg.Text)
		cells = append(cells, cluster{Start: offset, End: offset + runes, Width: seg.Width})
		offset += runes
	}
	return cells
}

// Width returns the terminal columns occupied by c.
func (c Clusters) Width() int {
	w := 0
	for _, cell := range c {
		w += cell.Width
	}
	return w
}

// At returns the cluster containing cursor, or len(c) At the end.
func (c Clusters) At(cursor int) int {
	for k, cell := range c {
		if cursor < cell.End {
			return k
		}
	}
	return len(c)
}

// RuneOffset returns the rune offset at cluster k's start.
func (c Clusters) RuneOffset(k int) int {
	switch {
	case k < len(c):
		return c[k].Start
	case len(c) > 0:
		return c[len(c)-1].End
	default:
		return 0
	}
}
