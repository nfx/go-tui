// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package spinner

import (
	"context"
	"errors"
	"testing"

	"github.com/nfx/go-tui/internal/ansi"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/terst"
)

func TestSpinnerUpdate(t *testing.T) {
	s, frames, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	spinner.Update("test message")
	tick()
	out := frames.Next()
	assert.Contains(t, out, "test")
	assert.Contains(t, out, "\r")

	spinner.Updatef("formatted %s %d", "message", 42)
	tick()
	out = frames.Next()
	assert.Contains(t, out, "forma")
	assert.Contains(t, out, "\x1b[1A\r\x1b[K")
}

func TestSpinnerFail(t *testing.T) {
	s, frames, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	testErr := errors.New("test error")
	spinner.Fail(testErr)
	tick()
	out := frames.Next()
	assert.Contains(t, out, "test")
}

func TestSpinnerWithPrefix(t *testing.T) {
	s, frames, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx, WithPrefixf("task-%d", 1))
	assert.NoError(t, err)

	spinner.Update("running")
	tick()
	out := frames.Next()
	assert.Contains(t, out, "task-")
}

func TestSpinnerWithKeep(t *testing.T) {
	s, frames, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx, WithKeep())
	assert.NoError(t, err)

	spinner.Update("kept spinner")
	tick()
	out := frames.Next()
	assert.Contains(t, out, "kept")

	err = spinner.Close()
	assert.NoError(t, err)

	tick()
	output := frames.Next()
	assert.Contains(t, output, "kept")
	assert.Contains(t, output, "\x1b[1A\r\x1b[K")
}

func TestSpinnerWithCustomFrames(t *testing.T) {
	th := terst.ASCIITheme()
	th.Symbols.Spinner = []string{"a", "b", "c", "d"}
	terst.UseTheme(t, th)
	s, frames, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	spinner.Update("custom")
	tick()
	assert.Equal(t, clipped("\rc custom\n\r"), frames.Next())
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
	s, frames, tick := spinnersForTest(t)
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
	assert.Equal(t, "", frames.Unread())
}

func TestSpinnerFailedState(t *testing.T) {
	s, frames, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	spinner, err := s.Add(ctx)
	assert.NoError(t, err)

	testErr := errors.New("failure")
	spinner.Fail(testErr)
	tick()
	assert.Equal(t, clipped("\r✖ failure\n\r"), frames.Next())

	// Failed spinner should remain visible even after close
	err = spinner.Close()
	assert.NoError(t, err)
	tick()
	output := frames.Next()
	assert.Contains(t, output, "failure")
	assert.Contains(t, output, "\x1b[1A\r\x1b[K")
}

func TestSpinnerOneLine(t *testing.T) {
	got := ansi.Text("a\r\nb\tc\x1b[2Kd\x1b[31mred\x1b[0m\xff\x07").OneLine()
	assert.Equal(t, "a  b cd\x1b[31mred\x1b[0m�", got)
}
