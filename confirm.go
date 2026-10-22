// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"

	"github.com/nfx/go-tui/internal/choice"
	"github.com/nfx/go-tui/internal/option"
)

// Confirmf is [Confirm] with a formatted action.
func Confirmf(ctx context.Context, format string, a ...any) bool {
	return choice.Confirmf(ctx, format, a...)
}

// Confirm asks to choose Yes or No and reports whether Yes was chosen. No is a
// normal answer, retained like Yes; an error also returns false.
func Confirm(ctx context.Context, action string, opts ...option.Opt) bool {
	return choice.Confirm(ctx, action, opts...)
}
