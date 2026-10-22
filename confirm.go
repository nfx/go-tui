// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"reflect"
)

var confirmRunner = defaultConfirmRunner

// Confirmf is [Confirm] with a formatted action.
func Confirmf(ctx context.Context, format string, a ...any) bool {
	return Confirm(ctx, fmt.Sprintf(format, a...))
}

// Confirm asks to choose Yes or No and reports whether Yes was chosen. No is a
// normal answer, retained like Yes; an error also returns false.
func Confirm(ctx context.Context, action string, opts ...Opt) bool {
	return confirmRunner(ctx, action, opts...)
}

// defaultConfirmRunner runs a dropdown over Yes and No through the Confirm slots.
func defaultConfirmRunner(ctx context.Context, action string, o ...Opt) bool {
	d := newDropdown(ctx)
	d.family = d.theme.Templates.Confirm
	d.target = targetOf[string]()
	d.normalize = confirmDefault
	err := Opts(o).Apply(d)
	if err != nil {
		return false
	}
	i, err := d.pick(ctx, action, []any{"Yes", "No"}) // Yes is first
	if err != nil {
		return false
	}
	return i == 0
}

// confirmDefault turns a boolean default into the text of its choice.
func confirmDefault(d any) any {
	if d == nil {
		return d
	}
	rv := reflect.ValueOf(d)
	if rv.Kind() != reflect.Bool {
		return d
	}
	if rv.Bool() {
		return "Yes"
	}
	return "No"
}
