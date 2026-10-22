// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

// The widgets own their view types. These mirror the fields that the built-in
// slots read, so the templates can be tested without importing a widget.

type fakeMultichoiceRow struct {
	Value            string
	Selected, Active bool
	Index            int
}

type fakeEmptyView struct{ Query string }

type fakeCollectionView struct {
	Values []string
	Count  int
}

type fakeSpinnerView struct {
	Frame   string
	Prefix  string
	Message string
	State   string
}

type fakeColumnView struct {
	Header string
	Name   string
	Kind   string
}

type fakeCellView struct {
	fakeColumnView
	Value string
}

type fakeProgressView struct {
	// Label is the caller label on one line; in Frame, the Label slot.
	Label string
	// Metrics is the Metrics slot, in the Frame of [ProgressStyle.Compact]
	// bars only.
	Metrics string
	// Bar is the bar, sized to the row, in Frame only.
	Bar     string
	Current int64
	// Total is the size of the work; not positive when unknown.
	Total int64
	// Percent is the completion, 0 to 100; 0 without a positive Total.
	Percent int
	// Amount is the bytes read of a file and, with a positive Total, its
	// size, in binary units: "3.0 MiB / 10.0 MiB". Empty for counted bars.
	Amount string
	// Rate is formatted by [WithFormatRate], without the "/s" unit; for a
	// file, in binary units without it.
	Rate      string
	Remaining string
	Elapsed   string
	// Values are the shown metrics with their units, in the order they are
	// kept: amount, rate, remaining and elapsed time.
	Values []string
	// Hints are the shown metrics as pairs of a value and its unit, in the
	// same order, led by the count of a counted bar with a total. The Hint
	// slot renders them.
	Hints HintView
}
