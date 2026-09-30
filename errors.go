// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import "errors"

var (
	ErrBug                 = errors.New("bug")
	ErrNoItems             = errors.New("no items")
	ErrInvalidState        = errors.New("invalid state")
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	ErrWrongWidget         = errors.New("wrong widget type")
)
