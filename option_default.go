// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
)

// defaultValue is a [WithDefault] answer. Presence is tracked apart from the
// content, as empty strings, zeros, false and nil are valid defaults.
type defaultValue struct {
	set bool
	raw any
}

// errNoDefault explains that a prompt without terminal has no automatic answer.
func errNoDefault(label string) error {
	return fmt.Errorf("%w: %q needs WithDefault when output is not a terminal", ErrNoTTY, label)
}

// errBadDefault wraps the reason a default cannot answer a prompt.
func errBadDefault(format string, a ...any) error {
	return fmt.Errorf("%w: default "+format, append([]any{ErrInvalidOption}, a...)...)
}
