// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"fmt"
)

// Progress configures progress bars: their geometry and their rows. The glyphs
// are [Symbols.BarFill], [Symbols.BarHead] and [Symbols.BarEmpty].
//
// Label and Metrics render first; Frame then places their output around the
// bar, which it must insert exactly once, as a top-level {{ .Bar }} that
// nothing else refers to, so that the bar can take the columns the rest of the
// row leaves.
//
// Metrics is the end of the bar row of [Progress.Compact] bars. Other bars
// show the metrics in the Hint row below the bar instead, and their Frame
// receives an empty .Metrics. Hint renders the hint pairs of the metrics, each
// with .Left and .Right, such as "6.2" and "MiB/s".
type Progress struct {
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
	// Label is the text before the bar.
	Label Template
	// Metrics is the values at the end of the compact bar row.
	Metrics Template
	// Hint is the row of metric pairs below a stacked bar.
	Hint Template
	// Frame places the label, percentage, bar and metrics on the bar row.
	Frame Template
}

// normalize takes 1 for a zero MinBarWidth and validates the widths.
func (s Progress) normalize() (Progress, error) {
	switch {
	case s.MinBarWidth < 0:
		return s, fmt.Errorf("theme.Progress.MinBarWidth: %d: must not be negative", s.MinBarWidth)
	case s.MaxBarWidth < 0:
		return s, fmt.Errorf("theme.Progress.MaxBarWidth: %d: must not be negative", s.MaxBarWidth)
	case s.MinBarWidth == 0:
		s.MinBarWidth = 1
	}
	if s.MaxBarWidth > 0 && s.MaxBarWidth < s.MinBarWidth {
		return s, fmt.Errorf("theme.Progress.MaxBarWidth: %d: below MinBarWidth %d", s.MaxBarWidth, s.MinBarWidth)
	}
	return s, nil
}
