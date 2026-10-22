// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"github.com/nfx/go-tui/internal/ansi"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestSpinnerUpdate(t *testing.T) {
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	spinner.Update("test message")
	tick()
	out := <-cio.Out
	assert.Contains(t, out, "test")
	assert.Contains(t, out, "\r")

	spinner.Updatef("formatted %s %d", "message", 42)
	tick()
	out = <-cio.Out
	assert.Contains(t, out, "forma")
	assert.Contains(t, out, "\x1b[1A\r\x1b[K")
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
	out := <-cio.Out
	assert.Contains(t, out, "test")
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
	out := <-cio.Out
	assert.Contains(t, out, "task-")
}

func TestSpinnerWithKeep(t *testing.T) {
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx, WithKeep())
	assert.NoError(t, err)

	spinner.Update("kept spinner")
	tick()
	out := <-cio.Out
	assert.Contains(t, out, "kept")

	err = spinner.Close()
	assert.NoError(t, err)

	tick()
	output := <-cio.Out
	assert.Contains(t, output, "kept")
	assert.Contains(t, output, "\x1b[1A\r\x1b[K")
}

func TestSpinnerWithCustomFrames(t *testing.T) {
	th := asciiTheme(false)
	th.Symbols.Spinner = []string{"a", "b", "c", "d"}
	terst.UseTheme(t, th)
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	spinner.Update("custom")
	tick()
	assert.Equal(t, "\rc custom\n\r", <-cio.Out)
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
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	testErr := errors.New("failure")
	spinner.Fail(testErr)
	tick()
	assert.Equal(t, "\r✖ failure\n\r", <-cio.Out)

	// Failed spinner should remain visible even after close
	err = spinner.Close()
	assert.NoError(t, err)
	tick()
	output := <-cio.Out
	assert.Contains(t, output, "failure")
	assert.Contains(t, output, "\x1b[1A\r\x1b[K")
}

func TestSpinnerOneLine(t *testing.T) {
	got := ansi.Text("a\r\nb\tc\x1b[2Kd\x1b[31mred\x1b[0m\xff\x07").OneLine()
	assert.Equal(t, "a  b cd\x1b[31mred\x1b[0m�", got)
}
