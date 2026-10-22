// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/errs"
	"math"
)

// Geometry is the row arithmetic of one prompt frame for a terminal size.
// Widgets keep drawing, clearing and caret movement; Geometry decides what
// fits. A zero width or height is unknown and unbounded.
type Geometry struct {
	// Inline keeps the label on the value row; otherwise the label has its
	// own row and the guide symbol leads the others.
	Inline        bool
	Guide         string
	Width, Height int
}

// lead returns the guide prefix for guided rows outside inline layouts.
func (g Geometry) lead(guided bool) ansi.Text {
	if !guided || g.Inline {
		return nil
	}
	return ansi.Text(g.Guide + " ")
}

// Columns returns space after lead, reserving the last column against autowrap;
// unknown widths are unbounded, and negative results mean lead does not fit.
func (g Geometry) Columns(lead ansi.Text) int {
	if g.Width <= 0 {
		return math.MaxInt
	}
	return g.Width - 1 - lead.Width()
}

// ValueLead returns the guide or an inline label clipped to reserve value and caret space.
func (g Geometry) ValueLead(label ansi.Text) ansi.Text {
	switch {
	case !g.Inline:
		return g.lead(true)
	case g.Width <= 0 || label.Width() <= g.Width-2:
		return label
	}
	return label.TruncateColumns(g.Width - 2)
}

// ValueColumns reserves the last column for the caret and returns ErrNoSpace
// if no value column fits after lead.
func (g Geometry) ValueColumns(lead ansi.Text) (int, error) {
	cols := g.Columns(lead)
	if cols < 1 {
		return 0, errs.ErrNoSpace
	}
	return cols, nil
}

// FrameRow is one physical row of a frame before fitting. A row that composes
// its own lead, such as the value row, is not guided.
type FrameRow struct {
	Content ansi.Text
	Guided  bool
	// Drop orders optional rows: when the height is short, the largest Drop
	// goes first, and of equal ones the later row. Zero is required.
	Drop int
}

// Fitted are the rows of a frame that fit, each with its lead and clipped.
type Fitted struct {
	Lines []ansi.Text
	// At is the position in lines of every row given to fit, or -1 when the
	// row was dropped.
	At []int
}

// Fit clips rows and drops empty or excess optional rows, preserving required
// rows even when empty; it returns ErrNoSpace if required rows cannot Fit.
func (g Geometry) Fit(rows []FrameRow) (Fitted, error) {
	keep := make([]bool, len(rows))
	kept, required := 0, 0
	for i, r := range rows {
		cols, visible := g.Columns(g.lead(r.Guided)), r.Content.Width() > 0
		switch {
		case r.Drop > 0 && (!visible || cols < 1):
			continue
		case r.Drop == 0 && (cols < 0 || cols < 1 && visible):
			return Fitted{}, errs.ErrNoSpace
		case r.Drop == 0:
			required++
		}
		keep[i] = true
		kept++
	}
	if g.Height > 0 && required > g.Height {
		return Fitted{}, errs.ErrNoSpace
	}
	for ; g.Height > 0 && kept > g.Height; kept-- {
		victim := -1
		for i, r := range rows {
			if keep[i] && r.Drop > 0 && (victim < 0 || r.Drop >= rows[victim].Drop) {
				victim = i
			}
		}
		keep[victim] = false
	}
	out := Fitted{Lines: make([]ansi.Text, 0, kept), At: make([]int, len(rows))}
	for i, r := range rows {
		if !keep[i] {
			out.At[i] = -1
			continue
		}
		lead := g.lead(r.Guided)
		content := r.Content
		if cols := g.Columns(lead); content.Width() > cols {
			content = content.TruncateColumns(cols)
		}
		out.At[i] = len(out.Lines)
		out.Lines = append(out.Lines, append(append(ansi.Text{}, lead...), content...))
	}
	return out, nil
}
