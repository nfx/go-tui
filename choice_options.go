// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"github.com/nfx/go-tui/internal/choice"
	"github.com/nfx/go-tui/internal/option"
)

func WithOneReturn() option.Opt {
	return choice.WithOneReturn()
}

// WithTemplate shares main content across choice rows and answers; details appear only on the active row.
func WithTemplate(main string, activeDetails ...string) option.Opt {
	return choice.WithTemplate(main, activeDetails...)
}
