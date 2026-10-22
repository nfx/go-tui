// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"maps"
	"slices"
	"text/template"
)

// Theme is the editable configuration of the appearance of every widget. A
// preset such as [NewTheme] is a complete, usable theme to edit.
type Theme struct {
	// Light forces the light palette variant without detection. Otherwise the
	// terminal background is detected, falling back to dark.
	Light bool
	// Inline keeps each prompt's label on its value row. Otherwise the label
	// has its own row, and the value, list and feedback rows start with the
	// guide symbol. Only geometry differs.
	Inline    bool
	Palette   Palette
	Symbols   Symbols
	Templates Templates
	Spinner   SpinnerStyle
	Progress  ProgressStyle
	Table     TableStyle
	JSON      JSONStyle
	// Funcs are custom template helpers, added to the built-in ones. They
	// cannot replace a built-in helper, and must be safe for concurrent use.
	Funcs template.FuncMap
}

// clone copies theme configuration without isolating helper closures.
func (t Theme) clone() Theme {
	t.Funcs = maps.Clone(t.Funcs)
	t.Symbols.Spinner = slices.Clone(t.Symbols.Spinner)
	return t
}

// presetTheme builds the default theme. The palette stays empty: [Palette.parse]
// fills every required color, and leaving the adaptive neutrals unset keeps
// them adapting to the background instead of becoming exact overrides.
func presetTheme() Theme {
	return Theme{
		Palette:  Palette{},
		Symbols:  unicodeSymbols.clone(),
		Spinner:  SpinnerStyle{Interval: spinnerInterval},
		Progress: progressStyle,
		Table:    tableStyle,
		JSON:     jsonStyle,
	}
}

// NewTheme returns an editable copy of the built-in theme with adaptive colors.
func NewTheme() Theme { return presetTheme() }
