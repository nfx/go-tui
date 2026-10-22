// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"fmt"
	"time"
)

const spinnerInterval = 100 * time.Millisecond

// SpinnerStyle is the timing of spinners. The frames are [Symbols.Spinner];
// the interval belongs to the whole [Spinners] group.
type SpinnerStyle struct {
	// Interval is the animation step. Zero takes 100ms.
	Interval time.Duration
}

// normalize fills the preset and validates the interval.
func (s SpinnerStyle) normalize() (SpinnerStyle, error) {
	switch {
	case s.Interval < 0:
		return s, fmt.Errorf("theme.Spinner.Interval: %v: must not be negative", s.Interval)
	case s.Interval == 0:
		s.Interval = spinnerInterval
	}
	return s, nil
}
