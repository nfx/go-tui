// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"math"
)

// geometry is the row arithmetic of one prompt frame for a terminal size.
// Widgets keep drawing, clearing and caret movement; geometry decides what
// fits. A zero width or height is unknown and unbounded.
type geometry struct {
	// inline keeps the label on the value row; otherwise the label has its
	// own row and the guide symbol leads the others.
	inline        bool
	guide         string
	width, height int
}

// lead returns the guide prefix for guided rows outside inline layouts.
func (g geometry) lead(guided bool) Text {
	if !guided || g.inline {
		return nil
	}
	return Text(g.guide + " ")
}

// columns returns space after lead, reserving the last column against autowrap;
// unknown widths are unbounded, and negative results mean lead does not fit.
func (g geometry) columns(lead Text) int {
	if g.width <= 0 {
		return math.MaxInt
	}
	return g.width - 1 - lead.Width()
}

// valueLead returns the guide or an inline label clipped to reserve value and caret space.
func (g geometry) valueLead(label Text) Text {
	switch {
	case !g.inline:
		return g.lead(true)
	case g.width <= 0 || label.Width() <= g.width-2:
		return label
	}
	return label.TruncateColumns(g.width - 2)
}

// valueColumns reserves the last column for the caret and returns ErrNoSpace
// if no value column fits after lead.
func (g geometry) valueColumns(lead Text) (int, error) {
	cols := g.columns(lead)
	if cols < 1 {
		return 0, ErrNoSpace
	}
	return cols, nil
}

// frameRow is one physical row of a frame before fitting. A row that composes
// its own lead, such as the value row, is not guided.
type frameRow struct {
	content Text
	guided  bool
	// drop orders optional rows: when the height is short, the largest drop
	// goes first, and of equal ones the later row. Zero is required.
	drop int
}

// fitted are the rows of a frame that fit, each with its lead and clipped.
type fitted struct {
	lines []Text
	// at is the position in lines of every row given to fit, or -1 when the
	// row was dropped.
	at []int
}

// fit clips rows and drops empty or excess optional rows, preserving required
// rows even when empty; it returns ErrNoSpace if required rows cannot fit.
func (g geometry) fit(rows []frameRow) (fitted, error) {
	keep := make([]bool, len(rows))
	kept, required := 0, 0
	for i, r := range rows {
		cols, visible := g.columns(g.lead(r.guided)), r.content.Width() > 0
		switch {
		case r.drop > 0 && (!visible || cols < 1):
			continue
		case r.drop == 0 && (cols < 0 || cols < 1 && visible):
			return fitted{}, ErrNoSpace
		case r.drop == 0:
			required++
		}
		keep[i] = true
		kept++
	}
	if g.height > 0 && required > g.height {
		return fitted{}, ErrNoSpace
	}
	for ; g.height > 0 && kept > g.height; kept-- {
		victim := -1
		for i, r := range rows {
			if keep[i] && r.drop > 0 && (victim < 0 || r.drop >= rows[victim].drop) {
				victim = i
			}
		}
		keep[victim] = false
	}
	out := fitted{lines: make([]Text, 0, kept), at: make([]int, len(rows))}
	for i, r := range rows {
		if !keep[i] {
			out.at[i] = -1
			continue
		}
		lead := g.lead(r.guided)
		content := r.content
		if cols := g.columns(lead); content.Width() > cols {
			content = content.TruncateColumns(cols)
		}
		out.at[i] = len(out.lines)
		out.lines = append(out.lines, append(append(Text{}, lead...), content...))
	}
	return out, nil
}
