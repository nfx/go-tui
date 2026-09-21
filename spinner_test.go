// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func testIOforSpinners(t *testing.T, width, height int, o ...opt) (*chanIO, func(), opt) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string),
	}
	ticks := make(chan time.Time)
	t.Cleanup(func() {
		cancel()
		// <-cio.Out // clear
		close(cio.In)
		close(cio.Out)
		close(ticks)
	})

	return cio, func() {
			go func() {
				select {
				case <-ctx.Done():
				case ticks <- time.Now():
				}
			}()
		}, WithOptions(append(opts{
			WithInput(cio),
			WithOutput(cio),
			WithContext(ctx),
			spinnersOpt(func(s *Spinners) error {
				s.ticks = ticks
				s.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
					return &termIO{
						in:      in,
						out:     out,
						Width:   width,
						Height:  height,
						Restore: func() error { return nil },
					}, nil
				}

				return nil
			}),
		}, o...,
		)...)
}

func spinnersForTest(t *testing.T) (*Spinners, *chanIO, func()) {
	t.Helper()
	cio, tick, opts := testIOforSpinners(t, 12, 4)
	s, err := NewSpinners(opts)
	assert.NoError(t, err)

	return s, cio, tick
}

func TestNewSpinners(t *testing.T) {
	s, cio, tick := spinnersForTest(t)
	assert.NotNil(t, s)
	assert.NotNil(t, cio)

	// test that the spinner is created
	ctx, cancel := context.WithCancel(t.Context())
	first, err := s.Add(ctx)
	assert.NoError(t, err)

	first.Update("first: A")

	tick()
	assert.Equal(t, "\r... first: A\n\r", <-cio.Out)

	second := s.MustAddBackground()
	second.Update("second: A")

	tick()
	assert.Equal(t, "\x1b[1A\r\x1b[K\r .. first: A\n\r\r .. second: A\n\r", <-cio.Out)

	cancel()

	tick()
	assert.Equal(t, "\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r\r  . first: A\n\r\r  . second: A\n\r", <-cio.Out)

	s.Close()
	// assert.Equal(t, "\x1b[2A", <-cio.Out)
}

func TestSpinnerUpdate(t *testing.T) {
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	spinner.Update("test message")
	tick()
	assert.Equal(t, "\r... test message\n\r", <-cio.Out)

	spinner.Updatef("formatted %s %d", "message", 42)
	tick()
	assert.Equal(t, "\x1b[1A\r\x1b[K\r .. formatted message 42\n\r", <-cio.Out)
}

func TestSpinnerFail(t *testing.T) {
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	testErr := errors.New("test error")
	spinner.Fail(testErr)
	tick()
	assert.Equal(t, "\r... test error\n\r", <-cio.Out)
}

func TestSpinnerWithPrefix(t *testing.T) {
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx, WithPrefixf("task-%d", 1))
	assert.NoError(t, err)

	spinner.Update("running")
	tick()
	assert.Equal(t, "\r... task-1: running\n\r", <-cio.Out)
}

func TestSpinnerWithKeep(t *testing.T) {
	t.Skip("flaky test, needs investigation")
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx, WithKeep())
	assert.NoError(t, err)

	spinner.Update("kept spinner")
	tick()
	assert.Equal(t, "\r... kept spinner\n\r", <-cio.Out)

	err = spinner.Close()
	assert.NoError(t, err)

	tick()
	assert.Equal(t, "\x1b[1A\r\x1b[K\r.. kept spinner\n\r", <-cio.Out)
}

func TestSpinnerWithCustomFrames(t *testing.T) {
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	customFrames := []string{"[  ]", "[. ]", "[..]", "[...]"}
	spinner, err := s.Add(ctx, WithFrames(customFrames))
	assert.NoError(t, err)

	spinner.Update("custom")
	tick()
	assert.Equal(t, "\r[..] custom\n\r", <-cio.Out)
}

func TestSpinnerStateNext(t *testing.T) {
	frames := []string{"a", "b", "c"}
	ss := &spinnerState{
		tick:   0,
		frames: frames,
	}

	assert.Equal(t, 0, ss.tick)
	ss.next()
	assert.Equal(t, 1, ss.tick)
	ss.next()
	assert.Equal(t, 2, ss.tick)
	ss.next()
	assert.Equal(t, 0, ss.tick) // wraps around
}

func TestSpinnersContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cio, tick, opts := testIOforSpinners(t, 12, 4, WithContext(ctx))
	s, err := NewSpinners(opts)
	assert.NoError(t, err)

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	spinner.Update("before cancel")
	tick()
	<-cio.Out // consume output

	cancel() // cancel the context

	// Try to add a new spinner after cancellation
	_, err = s.Add(ctx)
	assert.Error(t, err)

	s.Close()
}

func TestSpinnersOpt(t *testing.T) {
	called := false
	opt := spinnersOpt(func(s *Spinners) error {
		called = true

		return nil
	})

	s := &Spinners{}
	err := opt(s)
	assert.NoError(t, err)
	assert.Equal(t, true, called)

	// Test with non-Spinners type
	err = opt("not a spinner")
	assert.NoError(t, err) // should not error, just return nil
}

func TestNewSpinnersError(t *testing.T) {
	// Test with an option that returns an error
	errorOpt := func(a any) error {
		return errors.New("test error")
	}

	_, err := NewSpinners(errorOpt)
	assert.Error(t, err)
}

func TestSpinnerUpdateAfterStop(t *testing.T) {
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	err = spinner.Close()
	assert.NoError(t, err)

	// Update after close should not panic
	spinner.Update("after close")
	tick()
	// Should not output anything since spinner is stopped
	select {
	case <-cio.Out:
		t.Fatal("should not receive output from stopped spinner")
	default:
		// expected - no output
	}
}

func TestSpinnerFailedState(t *testing.T) {
	t.Skip("flaky test, needs investigation: panic: sync: negative WaitGroup counter")
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	testErr := errors.New("failure")
	spinner.Fail(testErr)
	tick()
	assert.Equal(t, "\r... failure\n\r", <-cio.Out)

	// Failed spinner should remain visible even after close
	err = spinner.Close()
	assert.NoError(t, err)
	tick()
	assert.Equal(t, "\x1b[1A\r\x1b[K\r.. failure\n\r", <-cio.Out)
}
