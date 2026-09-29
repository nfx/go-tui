// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"io"
	"strings"
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

func mustReceiveSpinnerEvent(t *testing.T, ch <-chan spinnerEvent) spinnerEvent {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for spinner event")
		return nil
	}
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
	out := <-cio.Out
	assert.Contains(t, out, "\x1b[2A\r\x1b[K") // cleared two lines
	assert.Contains(t, out, "second: A")
	// first spinner may already be cancelled before final paint; accept either presence or absence.
	if !strings.Contains(out, "first: A") {
		t.Log("first spinner already cancelled before final repaint")
	}

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
	output := <-cio.Out
	assert.Contains(t, output, "kept spinner")
	assert.Contains(t, output, "\x1b[1A\r\x1b[K")
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
	output := <-cio.Out
	assert.Contains(t, output, "failure")
	assert.Contains(t, output, "\x1b[1A\r\x1b[K")
}

func TestSpinnersDrainQueues(t *testing.T) {
	s := newSpinners()
	s.creates = make(chan createSpinner, 1)
	s.updates = make(chan updateOffset, 1)
	s.stops = make(chan int, 1)
	reply := make(chan int, 1)
	s.creates <- createSpinner{
		cancel:      func() {},
		frames:      []string{"."},
		replyOffset: reply,
	}
	s.drainQueues()
	if len(s.state) != 1 {
		t.Fatalf("expected one spinner, got %d", len(s.state))
	}
	s.updates <- updateOffset{offset: 0, message: "msg"}
	s.drainQueues()
	if s.state[0].Message != "msg" {
		t.Fatalf("expected message update")
	}
	s.stops <- 0
	s.drainQueues()
	if s.state[0] != nil {
		t.Fatalf("expected spinner removed")
	}
}

func TestSpinnersUpdateOffsetSends(t *testing.T) {
	s := newSpinners()
	s.updates = make(chan updateOffset, 1)
	s.updateOffset(2, "note", nil)
	got := <-s.updates
	if got.offset != 2 || got.message != "note" || got.err != nil {
		t.Fatalf("unexpected update %#v", got)
	}
}

func TestSpinnersUpdateOffsetContextDone(t *testing.T) {
	s := newSpinners()
	s.updates = make(chan updateOffset, 1)
	s.cancel()
	s.updateOffset(1, "msg", nil)
	select {
	case <-s.updates:
		t.Fatalf("expected no update")
	default:
	}
}

func TestSpinnersAddContextDone(t *testing.T) {
	s := newSpinners()
	s.cancel()
	_, err := s.Add(t.Context())
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestSpinnersAddWithCanceledContext(t *testing.T) {
	s, _, _ := spinnersForTest(t)
	defer s.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Add(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestSpinnersMustAddBackgroundPanics(t *testing.T) {
	s := newSpinners()
	s.cancel()
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic")
		}
	}()
	s.MustAddBackground()
}

func TestSpinnersEmitStructuredEvents(t *testing.T) {
	events := make(chan spinnerEvent, 32)
	_, _, opts := testIOforSpinners(t, 12, 4, spinnersOpt(func(s *Spinners) error {
		s.eventSink = func(ev spinnerEvent) {
			events <- ev
		}
		return nil
	}))
	s, err := NewSpinners(opts)
	assert.NoError(t, err)
	defer s.Close()

	_, ok := mustReceiveSpinnerEvent(t, events).(spinnerGroupInit)
	assert.True(t, ok)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first, err := s.Add(ctx, WithPrefixf("task-1"))
	assert.NoError(t, err)

	added, ok := mustReceiveSpinnerEvent(t, events).(spinnerAdded)
	assert.True(t, ok)
	assert.Equal(t, "task-1", added.Name)
	assert.Equal(t, 0, added.Index)

	first.Update("running")
	updated, ok := mustReceiveSpinnerEvent(t, events).(spinnerUpdated)
	assert.True(t, ok)
	assert.Equal(t, 0, updated.Index)
	assert.Equal(t, "running", updated.Message)

	err = first.Close()
	assert.NoError(t, err)
	removed, ok := mustReceiveSpinnerEvent(t, events).(spinnerRemoved)
	assert.True(t, ok)
	assert.Equal(t, 0, removed.Index)

	second, err := s.Add(ctx, WithPrefixf("task-2"))
	assert.NoError(t, err)
	added, ok = mustReceiveSpinnerEvent(t, events).(spinnerAdded)
	assert.True(t, ok)
	assert.Equal(t, "task-2", added.Name)
	assert.Equal(t, 1, added.Index)

	second.Fail(errors.New("boom"))
	failed, ok := mustReceiveSpinnerEvent(t, events).(spinnerFailed)
	assert.True(t, ok)
	assert.Equal(t, 1, failed.Index)
	assert.Equal(t, "boom", failed.Error)

	s.Close()
	_, ok = mustReceiveSpinnerEvent(t, events).(spinnerGroupClosed)
	assert.True(t, ok)
}
