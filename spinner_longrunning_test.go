// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"github.com/nfx/go-tui/internal/option"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestLongRunningShowsMessageWhileCbRuns(t *testing.T) {
	cio, tick, spinnerOpts := testIOforSpinners(t, 24, 5)
	testLongRunningWithNewSpinners(t, func(ctx context.Context, opt ...option.Opt) (*Spinners, error) {
		return NewSpinners(ctx, append(option.Opts{spinnerOpts}, opt...)...)
	})
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- LongRunning(t.Context(), "refreshing SWS scores", func(ctx context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("LongRunning returned early: %v", err)
	default:
	}
	tick()
	assert.Contains(t, <-cio.Out, "refreshing SWS")
	close(release)
	assert.NoError(t, <-done)
}

func TestLongRunningFallsBackWhenSpinnerSetupFails(t *testing.T) {
	testLongRunningWithNewSpinners(t, func(context.Context, ...option.Opt) (*Spinners, error) {
		return nil, errors.New("spinner setup failed")
	})
	expected := errors.New("callback failed")
	called := false
	err := LongRunning(t.Context(), "ignored", func(context.Context) error {
		called = true
		return expected
	})
	assert.True(t, called)
	assert.ErrorIs(t, err, expected)
}
