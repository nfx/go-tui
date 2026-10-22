// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/option"
)

var longRunningNewSpinners = NewSpinners

// LongRunning shows a spinner with the provided message while cb executes.
func LongRunning(ctx context.Context, message string, cb func(context.Context) error) error {
	spinners, err := longRunningNewSpinners(ctx)
	if err != nil {
		return cb(ctx)
	}
	defer spinners.Close()
	spinner, err := spinners.Add(ctx)
	if err != nil {
		return cb(ctx)
	}
	spinner.Update(message)
	return cb(ctx)
}

func NewSpinners(ctx context.Context, opt ...option.Opt) (*Spinners, error) {
	s := newSpinners(ctx)
	started := false
	defer func() {
		if !started {
			s.cancel(nil)
			s.ticker.Stop()
		}
	}()
	err := option.Opts(opt).Apply(s)
	if err != nil {
		return nil, err
	}
	// output only: no lease, so no probe
	look := defaultIO.bindTheme(ctx, s.theme, s.Out, nil)
	s.rows, err = bindSpinnerSlots(s.theme.Templates.Spinner, look.Funcs)
	if err != nil {
		return nil, err
	}
	s.io, err = s.makeTermIO(s.In, s.Out)
	if err != nil {
		if !errors.Is(err, ErrNoTTY) {
			return nil, err
		}
		s.io = nil
		s.ticker.Stop()
		s.ticks = nil // stage changes need no animation ticks
	} else if err = s.io.restoreMode(); err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	s.emit(spinnerGroupInit{})
	started = true
	go s.start(s.Ctx) //nolint:contextcheck // s.ctx derives from ctx and is canceled by Close
	return s, nil
}

// WithPrefixf sets the text shown next to a spinner added with [Spinners.Add].
func WithPrefixf(prefix string, args ...any) option.Opt {
	// TODO: decide if we expose text/template or fmt. This is a bit of a mess
	return option.OpT(func(cs *createSpinner) error {
		cs.prefix = fmt.Sprintf(prefix, args...)
		return nil
	})
}

// WithKeep will keep the spinner displayed after it's done.
func WithKeep() option.Opt {
	return option.OpT(func(cs *createSpinner) error {
		cs.keep = true
		return nil
	})
}
