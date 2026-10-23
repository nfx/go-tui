// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

// Package theme describes how every go-tui widget looks: colors, symbols,
// templates and layout. Start from [New], edit the copy, and install it with
// tui.SetDefaultTheme.
package theme

import (
	"fmt"
	"maps"
	"slices"
	"text/template"
)

// Theme is the editable configuration of the appearance of every widget. A
// preset such as [New] is a complete, usable theme to edit.
type Theme struct {
	// Light forces the light palette variant without detection. Otherwise the
	// terminal background is detected, falling back to dark.
	Light bool
	// Inline keeps each prompt's label on its value row. Otherwise the label
	// has its own row, and the value, list and feedback rows start with the
	// guide symbol. Only geometry differs.
	Inline      bool
	Palette     Palette
	Symbols     Symbols
	Prompt      Prompt
	Choice      Choice
	Input       Input
	Password    Password
	Number      Number
	Dropdown    Dropdown
	Multichoice Multichoice
	Confirm     Confirm
	FilePicker  FilePicker
	Spinner     Spinner
	Progress    Progress
	Table       Table
	JSON        JSON
	// Funcs are custom template helpers, added to the built-in ones. They
	// cannot replace a built-in helper, and must be safe for concurrent use.
	Funcs template.FuncMap
}

// Clone returns a copy of t that shares nothing editable with it: the helper map
// and the spinner frames are copied. The helper closures themselves are shared.
func (t Theme) Clone() Theme {
	t.Funcs = maps.Clone(t.Funcs)
	t.Symbols.Spinner = slices.Clone(t.Symbols.Spinner)
	return t
}

// Normalize validates t and returns an independent copy in which every zero
// symbol and style takes its default, and every valid palette color is spelled
// as canonical "#RRGGBB". Palette colors are not validated here: use
// [RGB.Validate]. The returned errors name the field path, for example
// "symbols: theme.Symbols.Focus: ...".
func (t Theme) Normalize() (Theme, error) {
	t = t.Clone()
	t.Palette = t.Palette.canonical()
	var err error
	t.Symbols, err = t.Symbols.normalize()
	if err != nil {
		return Theme{}, fmt.Errorf("symbols: %w", err)
	}
	t.Spinner, err = t.Spinner.normalize()
	if err != nil {
		return Theme{}, err
	}
	t.Progress, err = t.Progress.normalize()
	if err != nil {
		return Theme{}, err
	}
	t.Table, err = t.Table.normalize()
	if err != nil {
		return Theme{}, err
	}
	t.JSON, err = t.JSON.normalize()
	if err != nil {
		return Theme{}, err
	}
	return t, nil
}

// presetTheme builds the default theme. The palette stays empty: the palette parser of the render package
// fills every required color, and leaving the adaptive neutrals unset keeps
// them adapting to the background instead of becoming exact overrides.
func presetTheme() Theme {
	return Theme{
		Palette:  Palette{},
		Symbols:  unicodeSymbols.clone(),
		Spinner:  Spinner{Interval: spinnerInterval},
		Progress: Progress{MinBarWidth: 1},
		Table:    Table{ColumnGap: 2, MinColumnWidth: 1},
		JSON:     JSON{Indent: "  "},
	}
}

// New returns an editable copy of the built-in theme with adaptive colors.
func New() Theme { return presetTheme() }
