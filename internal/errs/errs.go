// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

// Package errs declares the sentinel errors of go-tui. The root package
// re-exports the same values, so errors.Is matches either spelling.
package errs

import "errors"

var (
	ErrBug                 = errors.New("bug")
	ErrNoItems             = errors.New("no items")
	ErrInvalidState        = errors.New("invalid state")
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	ErrWrongWidget         = errors.New("wrong widget type")
	ErrNoTTY               = errors.New("no tty")
	ErrNoSpace             = errors.New("no space in terminal")
	ErrEmptyLazyResult     = errors.New("empty lazy result")
	ErrInvalidOption       = errors.New("invalid option")
)
