// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"fmt"
)

// TableStyle is the layout of tables.
type TableStyle struct {
	// ColumnGap is the number of blank columns between the widest values
	// of neighbouring columns, including the one a truncated value keeps.
	// Zero takes 2.
	ColumnGap int
	// MinColumnWidth is the narrowest a column shrinks to fit the row
	// width, without the gap. Zero takes 1.
	MinColumnWidth int
}

// tableStyle is the theme preset: the legacy layout.
var tableStyle = TableStyle{ColumnGap: 2, MinColumnWidth: 1}

// normalize fills the preset and rejects negative sizes.
func (s TableStyle) normalize() (TableStyle, error) {
	for _, f := range []struct {
		name string
		v    *int
		def  int
	}{
		{"ColumnGap", &s.ColumnGap, tableStyle.ColumnGap},
		{"MinColumnWidth", &s.MinColumnWidth, tableStyle.MinColumnWidth},
	} {
		switch {
		case *f.v < 0:
			return s, fmt.Errorf("theme.Table.%s: %d: must not be negative", f.name, *f.v)
		case *f.v == 0:
			*f.v = f.def
		}
	}
	return s, nil
}
