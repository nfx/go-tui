// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"fmt"
	"time"
)

const spinnerInterval = 100 * time.Millisecond

// Spinner configures spinners: their timing and their rows in each state of
// the lifecycle. The frames are [Symbols.Spinner].
type Spinner struct {
	// Interval is the animation step. Zero takes 100ms.
	Interval time.Duration

	// Running is the row of a spinning spinner.
	Running Template
	// Stopped is the row of a spinner closed and kept with [WithKeep]; not a success.
	Stopped Template
	// Failed is the row of a failed spinner.
	Failed Template
}

// normalize fills the preset and validates the interval.
func (s Spinner) normalize() (Spinner, error) {
	switch {
	case s.Interval < 0:
		return s, fmt.Errorf("theme.Spinner.Interval: %v: must not be negative", s.Interval)
	case s.Interval == 0:
		s.Interval = spinnerInterval
	}
	return s, nil
}
