// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import "github.com/nfx/go-tui/internal/errs"

var (
	ErrBug                 = errs.ErrBug
	ErrNoItems             = errs.ErrNoItems
	ErrInvalidState        = errs.ErrInvalidState
	ErrUnsupportedPlatform = errs.ErrUnsupportedPlatform
	ErrWrongWidget         = errs.ErrWrongWidget
	// ErrNoTTY is returned when a prompt needs a terminal and there is none.
	ErrNoTTY = errs.ErrNoTTY
	// ErrNoSpace is returned when the terminal is too small to draw the widget.
	ErrNoSpace = errs.ErrNoSpace
	// ErrEmptyLazyResult is empty lazy result.
	ErrEmptyLazyResult = errs.ErrEmptyLazyResult
	// ErrInvalidOption identifies invalid configuration values.
	ErrInvalidOption = errs.ErrInvalidOption
)
