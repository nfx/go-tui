// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package spinner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"fmt"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func testIOforSpinners(t *testing.T, width, height int, o ...option.Opt) (*term.ChanIO, func(), option.Opt) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cio := &term.ChanIO{
		Ctx: ctx,
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
	terst.UseIO(t, cio, cio)
	return cio, func() {
			go func() {
				select {
				case <-ctx.Done():
				case ticks <- time.Now():
				}
			}()
		}, option.WithOptions(append(option.Opts{
			option.OpT(func(s *Spinners) error {
				s.ticks = ticks
				s.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
					return &term.IO{
						In:      in,
						Out:     out,
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

func spinnersForTest(t *testing.T) (*Spinners, *term.ChanIO, func()) {
	t.Helper()
	cio, tick, opts := testIOforSpinners(t, 12, 4)
	s, err := NewSpinners(cio.Ctx, opts)
	assert.NoError(t, err)
	return s, cio, tick
}

func testLongRunningWithNewSpinners(t *testing.T, cb func(context.Context, ...option.Opt) (*Spinners, error)) {
	t.Helper()
	prev := longRunningNewSpinners
	longRunningNewSpinners = cb
	t.Cleanup(func() {
		longRunningNewSpinners = prev
	})
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
	firstOut := <-cio.Out
	assert.Contains(t, firstOut, "▝ first: A")

	second := s.MustAddBackground()
	second.Update("second: A")

	tick()
	secondOut := <-cio.Out
	assert.Contains(t, secondOut, "\x1b[1A\r\x1b[K")
	assert.Contains(t, secondOut, "▐ first: A")
	assert.Contains(t, secondOut, "▐ second: A")

	cancel()

	tick()
	out := <-cio.Out
	assert.Contains(t, out, "\x1b[2A\r\x1b[K") // cleared two lines
	assert.Contains(t, out, "▗ second: A")
	// first spinner may already be cancelled before final paint; accept either presence or absence.
	if !strings.Contains(out, "first: A") {
		t.Log("first spinner already cancelled before final repaint")
	}

	s.Close()
	// assert.Equal(t, "\x1b[2A", <-cio.Out)
}

func TestSpinnersContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cio, tick, opts := testIOforSpinners(t, 12, 4)
	s, err := NewSpinners(ctx, opts)
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
	opt := option.OpT(func(s *Spinners) error {
		called = true
		return nil
	})

	s := &Spinners{theme: term.Default.CurrentTheme()}
	err := opt(s)
	assert.NoError(t, err)
	assert.Equal(t, true, called)

	// Test with non-Spinners type
	err = opt("not a spinner")
	assert.ErrorIs(t, err, errs.ErrWrongWidget)
}

func TestNewSpinnersError(t *testing.T) {
	// Test with an option that returns an error
	errorOpt := func(a any) error {
		return errors.New("test error")
	}

	_, err := NewSpinners(t.Context(), errorOpt)
	assert.Error(t, err)
}

func TestSpinnersDrainQueues(t *testing.T) {
	s := newSpinners(t.Context())
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
	if s.state[0].Message.String() != "msg" {
		t.Fatalf("expected message update")
	}
	s.stops <- 0
	s.drainQueues()
	if s.state[0] != nil {
		t.Fatalf("expected spinner removed")
	}
}

func TestSpinnersUpdateOffsetSends(t *testing.T) {
	s := newSpinners(t.Context())
	s.updates = make(chan updateOffset, 1)
	s.updateOffset(2, "note", nil)
	got := <-s.updates
	if got.offset != 2 || got.message != "note" || got.err != nil {
		t.Fatalf("unexpected update %#v", got)
	}
}

func TestSpinnersUpdateOffsetContextDone(t *testing.T) {
	s := newSpinners(t.Context())
	s.updates = make(chan updateOffset, 1)
	s.cancel(nil)
	s.updateOffset(1, "msg", nil)
	select {
	case <-s.updates:
		t.Fatalf("expected no update")
	default:
	}
}

func TestSpinnersAddContextDone(t *testing.T) {
	s := newSpinners(t.Context())
	s.cancel(nil)
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
	s := newSpinners(t.Context())
	s.cancel(nil)
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic")
		}
	}()
	s.MustAddBackground()
}

func TestSpinnersEmitStructuredEvents(t *testing.T) {
	events := make(chan spinnerEvent, 32)
	_, _, opts := testIOforSpinners(t, 12, 4, option.OpT(func(s *Spinners) error {
		s.eventSink = func(ev spinnerEvent) {
			events <- ev
		}
		return nil
	}))
	s, err := NewSpinners(t.Context(), opts)
	assert.NoError(t, err)
	defer s.Close()

	_, ok := terst.Recv(t, events).(spinnerGroupInit)
	assert.True(t, ok)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first, err := s.Add(ctx, WithPrefixf("task-1"))
	assert.NoError(t, err)

	added, ok := terst.Recv(t, events).(spinnerAdded)
	assert.True(t, ok)
	assert.Equal(t, "task-1", added.Name)
	assert.Equal(t, 0, added.Index)

	first.Update("running")
	updated, ok := terst.Recv(t, events).(spinnerUpdated)
	assert.True(t, ok)
	assert.Equal(t, 0, updated.Index)
	assert.Equal(t, "running", updated.Message)

	err = first.Close()
	assert.NoError(t, err)
	removed, ok := terst.Recv(t, events).(spinnerRemoved)
	assert.True(t, ok)
	assert.Equal(t, 0, removed.Index)

	second, err := s.Add(ctx, WithPrefixf("task-2"))
	assert.NoError(t, err)
	added, ok = terst.Recv(t, events).(spinnerAdded)
	assert.True(t, ok)
	assert.Equal(t, "task-2", added.Name)
	assert.Equal(t, 1, added.Index)

	second.Fail(errors.New("boom"))
	failed, ok := terst.Recv(t, events).(spinnerFailed)
	assert.True(t, ok)
	assert.Equal(t, 1, failed.Index)
	assert.Equal(t, "boom", failed.Error)

	s.Close()
	_, ok = terst.Recv(t, events).(spinnerGroupClosed)
	assert.True(t, ok)
}

func TestSpinnersLogFallback(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	out, err := os.CreateTemp(t.TempDir(), "spinner-output")
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, out.Close()) })

	terst.UseIO(t, nil, out)
	s, err := NewSpinners(t.Context())
	assert.NoError(t, err)
	defer s.Close()

	first, err := s.Add(t.Context(), WithPrefixf("download"), WithKeep())
	assert.NoError(t, err)
	first.Update("fetching")
	first.Update("fetching")
	first.Updatef("processing %d files", 2)
	assert.NoError(t, first.Close())
	first.Update("ignored after close")
	assert.NoError(t, first.Close())

	second, err := s.Add(t.Context(), WithPrefixf("upload"))
	assert.NoError(t, err)
	second.Fail(errors.New("connection lost"))
	second.Fail(errors.New("ignored duplicate failure"))
	second.Update("ignored after failure")
	assert.NoError(t, second.Close())

	_, err = s.Add(t.Context(), WithPrefixf("pending"))
	assert.NoError(t, err)
	s.Close()
	_, err = s.Add(t.Context())
	assert.ErrorIs(t, err, context.Canceled)

	type record struct {
		Level   string `json:"level"`
		Message string `json:"msg"`
		Spinner int    `json:"spinner"`
		Prefix  string `json:"prefix"`
		Status  string `json:"status"`
	}
	var got []record
	decoder := json.NewDecoder(&logs)
	for decoder.More() {
		var entry record
		assert.NoError(t, decoder.Decode(&entry))
		got = append(got, entry)
	}
	assert.Equal(t, []record{
		{"INFO", "spinner started", 0, "download", "started"},
		{"INFO", "fetching", 0, "download", "running"},
		{"INFO", "processing 2 files", 0, "download", "running"},
		{"INFO", "spinner stopped", 0, "download", "stopped"},
		{"INFO", "spinner started", 1, "upload", "started"},
		{"ERROR", "connection lost", 1, "upload", "failed"},
		{"INFO", "spinner started", 2, "pending", "started"},
		{"INFO", "spinner stopped", 2, "pending", "stopped"},
	}, got)
	info, err := out.Stat()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), info.Size())
}

func TestSpinnersLogFallbackKeepsTerminalRendering(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cio, tick, options := testIOforSpinners(t, 24, 5)
	s, err := NewSpinners(t.Context(), options)
	assert.NoError(t, err)
	defer s.Close()
	spinner, err := s.Add(t.Context())
	assert.NoError(t, err)
	spinner.Update("fetching")
	tick()
	assert.Contains(t, <-cio.Out, "fetching")
	s.Close()
	assert.Equal(t, "", logs.String())
}

func TestSpinnersLogFallbackRejectsOtherErrors(t *testing.T) {
	_, err := NewSpinners(t.Context(), option.OpT(func(s *Spinners) error {
		s.makeTermIO = func(io.Reader, io.Writer) (*term.IO, error) {
			return nil, io.ErrClosedPipe
		}
		return nil
	}))
	assert.ErrorIs(t, err, io.ErrClosedPipe)
}

// themedSpinners is a spinner group rendering through th on a terminal of the
// given width, with manual ticks. Each write is one receive from frames.
// restored reports a released terminal.
func themedSpinners(
	t *testing.T,
	th theme.Theme,
	width int,
	o ...option.Opt,
) (*Spinners, terst.FrameWriter, func(), *atomic.Bool) {
	t.Helper()
	terst.UseTheme(t, th)
	restored := &atomic.Bool{}
	frames := make(terst.FrameWriter)
	cio, tick, options := testIOforSpinners(t, width, 10, append([]option.Opt{option.OpT(func(s *Spinners) error {
		s.makeTermIO = func(in io.Reader, _ io.Writer) (*term.IO, error) {
			return &term.IO{In: in, Out: frames, Width: width, Height: 10, Restore: func() error {
				restored.Store(true)
				return nil
			}}, nil
		}
		return nil
	})}, o...)...)
	s, err := NewSpinners(cio.Ctx, options)
	assert.NoError(t, err)
	t.Cleanup(func() {
		go func() {
			for range frames { // the final clear of a group a test left open
			}
		}()
		s.Close()
		close(frames)
	})
	return s, frames, tick, restored
}

// untilStopped returns everything the group writes until its loop ends,
// failing the test unless it ends within a second.
func untilStopped(t *testing.T, s *Spinners, frames terst.FrameWriter) string {
	t.Helper()
	var out strings.Builder
	timeout := time.After(time.Second)
	for {
		select {
		case f := <-frames:
			out.WriteString(f)
		case <-s.stopped:
			return out.String()
		case <-timeout:
			t.Fatal("spinner group did not stop")
		}
	}
}

func TestChanIO_Forward(t *testing.T) {
	t.SkipNow()
	cio, stdout := terst.ChainIOForTest(t, 12, 3)

	// write 5 lines
	_, err := fmt.Fprint(cio, "a\nb\nc\nd\ne\n")
	assert.NoError(t, err)

	// render 3 lines due to the viewport height
	assert.Equal(t, "\rc           \n\rd           \n\re           ", <-stdout.C)

	// write one more line
	_, err = fmt.Fprint(cio, "f\n")
	assert.NoError(t, err)

	// and have the previous two lines still rendered
	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rd           \n\re           \n\rf           ",
		<-stdout.C)

	ticks := make(chan time.Time)
	defer close(ticks)
	tick := func() {
		go func() {
			select {
			case <-cio.Ctx.Done():
			case ticks <- time.Now():
			}
		}()
	}

	terst.UseIO(t, nil, cio)
	s, err := NewSpinners(cio.Ctx,
		option.OpT(func(s *Spinners) error { //nolint:unparam // ..
			s.ticks = ticks
			return nil
		}),
	)
	assert.NoError(t, err)

	s.MustAddBackground().Update("s: A")
	tick()

	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\r\r... s: A   \n\r\r           \n\rf           ",
		<-stdout.C)

	// write one more line
	_, err = fmt.Fprint(cio, "g\n")
	assert.NoError(t, err)

	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\r\r... s: A   \n\r\r           \n\rg           ",
		<-stdout.C)
}

func TestNewSpinnersUsesDefaultIO(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() { term.Default.SetIO(prevIn, prevOut) })
	in := bytes.NewBufferString("in")
	out := &bytes.Buffer{}
	term.Default.SetIO(in, out)

	s := newSpinners(t.Context())
	assert.Equal(t, in, s.In)
	assert.Equal(t, out, s.Out)
	s.ticker.Stop()
}
