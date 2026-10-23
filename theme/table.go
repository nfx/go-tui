// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"cmp"
	"fmt"
)

// Table configures tables and facts: their layout and their cells.
type Table struct {
	// ColumnGap is the number of blank columns between the widest values
	// of neighbouring columns, including the one a truncated value keeps.
	// Zero takes 2.
	ColumnGap int
	// MinColumnWidth is the narrowest a column shrinks to fit the row
	// width, without the gap. Zero takes 1.
	MinColumnWidth int
	// Header is the header of a column, or the key of a fact. A hidden Header
	// drops the header row, or the keys.
	Header Template
	// Cell wraps each formatted value of an automatic row template, one
	// physical line at a time, after any column template, formatter or scale;
	// an explicit row template keeps its own presentation.
	Cell Template
}

// normalize takes 2 for a zero ColumnGap and 1 for a zero MinColumnWidth, and
// rejects negative sizes.
func (s Table) normalize() (Table, error) {
	switch {
	case s.ColumnGap < 0:
		return s, fmt.Errorf("theme.Table.ColumnGap: %d: must not be negative", s.ColumnGap)
	case s.MinColumnWidth < 0:
		return s, fmt.Errorf("theme.Table.MinColumnWidth: %d: must not be negative", s.MinColumnWidth)
	}
	s.ColumnGap = cmp.Or(s.ColumnGap, 2)
	s.MinColumnWidth = cmp.Or(s.MinColumnWidth, 1)
	return s, nil
}
