// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"fmt"
)

// ProgressStyle is the geometry of progress bars. Their glyphs are
// [Symbols.BarFill], [Symbols.BarHead] and [Symbols.BarEmpty].
type ProgressStyle struct {
	// MinBarWidth is the narrowest bar kept by dropping metrics, the last
	// first, or by shortening the label. Zero takes 1.
	MinBarWidth int
	// MaxBarWidth bounds the bar. Zero leaves it the rest of the row.
	MaxBarWidth int
	// Compact keeps the label, the percentage, the bar and the metrics on
	// one row, dropping metrics to keep the bar. Otherwise the label, the
	// percentage and the bar share a row and the metrics go to a hint row
	// below it; a terminal without room for both rows gets the compact row.
	Compact bool
}

// progressStyle is the theme preset.
var progressStyle = ProgressStyle{MinBarWidth: 1}

// normalize fills the preset and validates the widths.
func (s ProgressStyle) normalize() (ProgressStyle, error) {
	def := progressStyle
	switch {
	case s.MinBarWidth < 0:
		return s, fmt.Errorf("theme.Progress.MinBarWidth: %d: must not be negative", s.MinBarWidth)
	case s.MaxBarWidth < 0:
		return s, fmt.Errorf("theme.Progress.MaxBarWidth: %d: must not be negative", s.MaxBarWidth)
	case s.MinBarWidth == 0:
		s.MinBarWidth = def.MinBarWidth
	}
	if s.MaxBarWidth > 0 && s.MaxBarWidth < s.MinBarWidth {
		return s, fmt.Errorf("theme.Progress.MaxBarWidth: %d: below MinBarWidth %d", s.MaxBarWidth, s.MinBarWidth)
	}
	return s, nil
}
