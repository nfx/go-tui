// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"text/template"
)

// DefaultTheme returns an editable copy of the installed theme, preserving helpers and unset colors.
func DefaultTheme() Theme { return defaultIO.currentTheme().editable() }

// SetDefaultTheme validates and copies theme for operations started afterwards.
// On error, the installed theme is unchanged; running operations keep their snapshot.
// Funcs replaces existing helpers; start from [DefaultTheme] to retain them.
// Helper closures must be safe for concurrent use.
func SetDefaultTheme(theme Theme) error { return defaultIO.installTheme(theme) }

// WithFn registers or replaces a helper for operations started afterwards.
// Helpers must be safe for concurrent use and return a value or a value and an error.
// Invalid or reserved names and invalid signatures panic without changing the theme.
// Use [Theme.Funcs] with [SetDefaultTheme] to receive validation errors instead.
func WithFn(name string, fn any) {
	err := defaultIO.updateTheme(func(t *Theme) {
		if t.Funcs == nil {
			t.Funcs = template.FuncMap{}
		}
		t.Funcs[name] = fn
	})
	if err != nil {
		panic(fmt.Errorf("tui.WithFn: %w", err))
	}
}
