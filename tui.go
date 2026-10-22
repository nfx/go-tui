// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"

	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
)

// Tui is a terminal session: the widgets apply their options to it, and it
// exposes the raw terminal as an [io.ReadWriter].
type Tui = term.Tui

// NewTUI starts a terminal session on the standard streams.
func NewTUI(ctx context.Context, opts ...option.Opt) (*Tui, error) {
	return term.NewTUI(ctx, opts...)
}
