// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"github.com/nfx/go-tui/internal/errs"
)

// DefaultValue is a [WithDefault] answer. Presence is tracked apart from the
// content, as empty strings, zeros, false and nil are valid defaults.
type DefaultValue struct {
	set bool
	raw any
}

// NoDefault explains that a prompt without terminal has no automatic answer.
func NoDefault(label string) error {
	return fmt.Errorf("%w: %q needs WithDefault when output is not a terminal", errs.ErrNoTTY, label)
}

// BadDefault wraps the reason a default cannot answer a prompt.
func BadDefault(format string, a ...any) error {
	return fmt.Errorf("%w: default "+format, append([]any{errs.ErrInvalidOption}, a...)...)
}
