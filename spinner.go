// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"

	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/spinner"
)

// LongRunning shows a spinner with the provided message while cb executes.
func LongRunning(ctx context.Context, message string, cb func(context.Context) error) error {
	return spinner.LongRunning(ctx, message, cb)
}

func NewSpinners(ctx context.Context, opt ...option.Opt) (*Spinners, error) {
	return spinner.NewSpinners(ctx, opt...)
}

// WithPrefixf sets the text shown next to a spinner added with [Spinners.Add].
func WithPrefixf(prefix string, args ...any) option.Opt {
	return spinner.WithPrefixf(prefix, args...)
}

// WithKeep will keep the spinner displayed after it's done.
func WithKeep() option.Opt {
	return spinner.WithKeep()
}

// Spinners shows a group of spinners that share a terminal region.
type Spinners = spinner.Spinners

// Spinner is one line of a [Spinners] group.
type Spinner = spinner.Spinner
