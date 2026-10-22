// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

// Background describes terminal appearance; zero means the dark fallback.
type Background struct {
	// RGB is valid only when measured; otherwise use the variant's reference.
	RGB SRGB
	// Light selects the variant, using an estimate when unmeasured.
	Light bool
	// Measured distinguishes terminal RGB from palette estimates.
	Measured bool
}
