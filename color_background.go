// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

// background describes terminal appearance; zero means the dark fallback.
type background struct {
	// rgb is valid only when measured; otherwise use the variant's reference.
	rgb sRGB
	// light selects the variant, using an estimate when unmeasured.
	light bool
	// measured distinguishes terminal RGB from palette estimates.
	measured bool
}
