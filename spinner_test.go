// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

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
	"text/template"
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
			opT(func(s *Spinners) error {
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
	s, err := NewSpinners(cio.ctx, opts)
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

func testLongRunningWithNewSpinners(t *testing.T, cb func(context.Context, ...opt) (*Spinners, error)) {
	t.Helper()
	prev := longRunningNewSpinners
	longRunningNewSpinners = cb
	t.Cleanup(func() {
		longRunningNewSpinners = prev
	})
}

func TestLongRunningShowsMessageWhileCbRuns(t *testing.T) {
	cio, tick, spinnerOpts := testIOforSpinners(t, 24, 5)
	testLongRunningWithNewSpinners(t, func(ctx context.Context, opt ...opt) (*Spinners, error) {
		return NewSpinners(ctx, append(opts{spinnerOpts}, opt...)...)
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
	testLongRunningWithNewSpinners(t, func(context.Context, ...opt) (*Spinners, error) {
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
	assert.Contains(t, firstOut, "... first: A")

	second := s.MustAddBackground()
	second.Update("second: A")

	tick()
	secondOut := <-cio.Out
	assert.Contains(t, secondOut, "\x1b[1A\r\x1b[K")
	assert.Contains(t, secondOut, ".. first: A")
	assert.Contains(t, secondOut, ".. secon")

	cancel()

	tick()
	out := <-cio.Out
	assert.Contains(t, out, "\x1b[2A\r\x1b[K") // cleared two lines
	assert.Contains(t, out, ". secon")
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
	s, cio, tick := spinnersForTest(t)
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	customFrames := []string{"[   ]", "[.  ]", "[.. ]", "[...]"}
	spinner, err := s.Add(ctx, WithFrames(customFrames))
	assert.NoError(t, err)

	spinner.Update("custom")
	tick()
	assert.Equal(t, "\r[.. ] custom\n\r", <-cio.Out)
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
	opt := opT(func(s *Spinners) error {
		called = true
		return nil
	})

	s := &Spinners{}
	err := opt(s)
	assert.NoError(t, err)
	assert.Equal(t, true, called)

	// Test with non-Spinners type
	err = opt("not a spinner")
	assert.ErrorIs(t, err, ErrWrongWidget)
}

func TestNewSpinnersError(t *testing.T) {
	// Test with an option that returns an error
	errorOpt := func(a any) error {
		return errors.New("test error")
	}

	_, err := NewSpinners(t.Context(), errorOpt)
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
	_, _, opts := testIOforSpinners(t, 12, 4, opT(func(s *Spinners) error {
		s.eventSink = func(ev spinnerEvent) {
			events <- ev
		}
		return nil
	}))
	s, err := NewSpinners(t.Context(), opts)
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

func TestSpinnersAddRejectsEmptyFrames(t *testing.T) {
	s := &Spinners{}
	if _, err := s.Add(context.Background(), WithFrames(nil)); err == nil {
		t.Fatal("expected error for empty frames")
	}
}

func TestSpinnersAddRejectsUnequalFrames(t *testing.T) {
	s := &Spinners{}
	_, err := s.Add(context.Background(), WithFrames([]string{"..", "..."}))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spinner: frames[1]")
}

func TestSpinnersLogFallback(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	out, err := os.CreateTemp(t.TempDir(), "spinner-output")
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, out.Close()) })

	s, err := NewSpinners(t.Context(), WithOutput(out))
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
	_, err := NewSpinners(t.Context(), opT(func(s *Spinners) error {
		s.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) {
			return nil, io.ErrClosedPipe
		}
		return nil
	}))
	assert.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestCheckFrames(t *testing.T) {
	for _, frames := range [][]string{
		SpinnerStyleDocs,
		DefaultSpinnerStyle,
		{"\x1b[32m⠋\x1b[0m", "\x1b[32m⠙\x1b[0m"},
		{"\u754c", " ."},
		{"-"},
	} {
		assert.NoError(t, new(Spinners).checkFrames(frames))
	}
	for _, tc := range []struct {
		frames []string
		want   string
	}{
		{nil, "frames must not be empty"},
		{[]string{}, "frames must not be empty"},
		{[]string{""}, "frames[0]: \"\": want a positive display width"},
		{[]string{"\x1b[31m"}, "want a positive display width"},
		{[]string{"\u0301"}, "want a positive display width"},
		{[]string{"a", "a\nb"}, "frames[1]: \"a\\nb\": \"\\n\" is not text or SGR"},
		{[]string{"a\tb"}, "is not text or SGR"},
		{[]string{"\x1b[2Ka"}, "is not text or SGR"},
		{[]string{"ab", "abc"}, "frames[1]: \"abc\": width 3, want 2 as frames[0]"},
	} {
		err := new(Spinners).checkFrames(tc.frames)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

// frameWriter delivers every write as one receive. Unlike a [chanIO]
// without a viewport, spinners clear their rows on it when they stop.
type frameWriter chan string

func (w frameWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

// themedSpinners is a spinner group rendering through th on a terminal of the
// given width, with manual ticks. Each write is one receive from frames.
// restored reports a released terminal.
func themedSpinners(t *testing.T, th Theme, width int, o ...opt) (*Spinners, frameWriter, func(), *atomic.Bool) {
	t.Helper()
	useTheme(t, th)
	restored := &atomic.Bool{}
	frames := make(frameWriter)
	cio, tick, options := testIOforSpinners(t, width, 10, append([]opt{opT(func(s *Spinners) error {
		s.makeTermIO = func(in io.Reader, _ io.Writer) (*termIO, error) {
			return &termIO{in: in, out: frames, Width: width, Height: 10, Restore: func() error {
				restored.Store(true)
				return nil
			}}, nil
		}
		return nil
	})}, o...)...)
	s, err := NewSpinners(cio.ctx, options)
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
func untilStopped(t *testing.T, s *Spinners, frames frameWriter) string {
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

func TestThemedSpinnerStates(t *testing.T) {
	s, frames, tick, restored := themedSpinners(t, asciiTheme(false), 40)
	build, err := s.Add(t.Context(), WithPrefixf("build"), WithKeep())
	assert.NoError(t, err)
	lint, err := s.Add(t.Context(), WithPrefixf("lint"))
	assert.NoError(t, err)
	docs, err := s.Add(t.Context())
	assert.NoError(t, err)
	build.Update("compiling")
	lint.Update("checking")
	docs.Update("writing")
	tick()
	assert.Equal(t, "\r... build: compiling\n\r\r .. lint: checking\n\r\r  . writing\n\r", <-frames)

	// kept after Close is stopped, not a success; failed stays; the rest goes
	assert.NoError(t, build.Close())
	lint.Fail(errors.New("3 problems"))
	assert.NoError(t, docs.Close())
	tick()
	rows := "\r- build: compiling\n\r\r! lint: 3 problems\n\r"
	assert.Equal(t, "\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r"+rows, <-frames)
	// ended spinners no longer animate
	tick()
	assert.Equal(t, "\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r"+rows, <-frames)

	go s.Close()
	assert.Equal(t, "\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r", untilStopped(t, s, frames))
	assert.True(t, restored.Load())
}

func TestThemedSpinnerFramesPrecedenceAndGroupTheme(t *testing.T) {
	th := asciiTheme(false)
	th.Spinner.Frames = []string{"[a]", "[b]"}
	s, frames, tick, _ := themedSpinners(t, th, 40)
	themed, err := s.Add(t.Context())
	assert.NoError(t, err)
	themed.Update("theme")
	own, err := s.Add(t.Context(), WithFrames([]string{"<1>", "<2>", "<3>"}))
	assert.NoError(t, err)
	own.Update("own")

	// a theme installed later reaches neither the group nor its later spinners
	later := asciiTheme(false)
	later.Spinner.Frames = []string{"(x)"}
	later.Templates.Spinner.Running = `changed`
	assert.NoError(t, defaultIO.installTheme(later))
	added, err := s.Add(t.Context())
	assert.NoError(t, err)
	added.Update("later")
	tick()
	assert.Equal(t, "\r[a] theme\n\r\r<1> own\n\r\r[a] later\n\r", <-frames)
}

func TestThemedSpinnerSlotsAndView(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Spinner.Running = `{{ .State }} {{ .Frame }} {{ .Prefix }}={{ .Message }}`
	th.Templates.Spinner.Failed = `{{ .State }} {{ .Frame }} {{ .Message }}`
	s, frames, tick, _ := themedSpinners(t, th, 40)
	sp, err := s.Add(t.Context(), WithPrefixf("p"))
	assert.NoError(t, err)
	sp.Update("line\none\ttab\x1b[2K")
	tick()
	assert.Equal(t, "\rrunning ... p=line one tab\n\r", <-frames)
	sp.Fail(errors.New("bad"))
	tick()
	// the failed row shows the frame it stopped at
	assert.Equal(t, "\x1b[1A\r\x1b[K\rfailed ... bad\n\r", <-frames)
}

func TestThemedSpinnerClipsBeforeLastColumn(t *testing.T) {
	s, frames, tick, _ := themedSpinners(t, asciiTheme(false), 10)
	sp, err := s.Add(t.Context())
	assert.NoError(t, err)
	sp.Update("a long message")
	tick()
	assert.Equal(t, "\r... a lo…\n\r", <-frames)
}

func TestThemedSpinnerRoles(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	th := asciiTheme(false)
	th.Light = true
	s, frames, tick, _ := themedSpinners(t, th, 40)
	run, err := s.Add(t.Context())
	assert.NoError(t, err)
	run.Update("run")
	kept, err := s.Add(t.Context(), WithKeep())
	assert.NoError(t, err)
	assert.NoError(t, kept.Close())
	failed, err := s.Add(t.Context())
	assert.NoError(t, err)
	failed.Fail(errors.New("no"))
	tick()
	out := <-frames
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), s.out, nil).roles
	assert.True(t, len(roles.accent) > 0 && len(roles.muted) > 0 && len(roles.failure) > 0)
	assert.Contains(t, out, string(roles.accent)+"...")
	assert.Contains(t, out, string(roles.muted)+"-")
	assert.Contains(t, out, string(roles.failure)+"!")
	// Close is not success
	assert.True(t, !strings.Contains(out, string(roles.success)))
}

func TestThemedSpinnerRenderErrorEndsGroup(t *testing.T) {
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Spinner.Running = `{{ if eq .Message "bad" }}{{ boom }}{{ end }}{{ .Message }}`
	s, frames, tick, restored := themedSpinners(t, th, 40)
	sp, err := s.Add(t.Context())
	assert.NoError(t, err)
	sp.Update("ok")
	tick()
	assert.Equal(t, "\rok\n\r", <-frames)

	sp.Update("bad")
	tick()
	// no partial frame: only the drawn row is cleared
	assert.Equal(t, "\x1b[1A\r\x1b[K", untilStopped(t, s, frames))
	assert.True(t, restored.Load())
	_, err = s.Add(t.Context())
	assert.Contains(t, err.Error(), "theme.Templates.Spinner.Running")
	assert.Contains(t, err.Error(), "boom")
	err = sp.Close()
	assert.Contains(t, err.Error(), "boom")
	sp.Update("ignored") // does not block
}

func TestThemedSpinnerWriteErrorEndsGroup(t *testing.T) {
	gone := errors.New("terminal gone")
	var released atomic.Bool
	s, frames, tick, _ := themedSpinners(t, asciiTheme(false), 40, opT(func(s *Spinners) error {
		s.makeTermIO = func(in io.Reader, _ io.Writer) (*termIO, error) {
			return &termIO{in: in, out: failingWriter{gone}, Width: 40, Height: 10, Restore: func() error {
				released.Store(true)
				return nil
			}}, nil
		}
		return nil
	}))
	_, err := s.Add(t.Context())
	assert.NoError(t, err)
	tick()
	assert.Equal(t, "", untilStopped(t, s, frames))
	assert.True(t, released.Load())
	_, err = s.Add(t.Context())
	assert.ErrorIs(t, err, gone)
}

func TestThemedSpinnerInterval(t *testing.T) {
	realTicks := opT(func(s *Spinners) error {
		s.ticks = s.ticker.C
		return nil
	})
	th := asciiTheme(false)
	th.Spinner.Interval = time.Millisecond
	s, frames, _, _ := themedSpinners(t, th, 40, realTicks)
	_, err := s.Add(t.Context())
	assert.NoError(t, err)
	select {
	case <-frames:
	case <-time.After(time.Second):
		t.Fatal("no frame at the theme interval")
	}
	go s.Close()
	untilStopped(t, s, frames)

	th.Spinner.Interval = time.Hour
	s, frames, _, _ = themedSpinners(t, th, 40, realTicks)
	_, err = s.Add(t.Context())
	assert.NoError(t, err)
	select {
	case out := <-frames:
		t.Fatalf("frame before the theme interval: %q", out)
	case <-time.After(300 * time.Millisecond): // three legacy intervals
	}
}

func TestThemedSpinnerLogsWithoutTerminal(t *testing.T) {
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Spinner.Running = `{{ boom }}`
	th.Templates.Spinner.Failed = `{{ boom }}`
	useTheme(t, th)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	var out bytes.Buffer
	s, err := NewSpinners(t.Context(), WithOutput(&out))
	assert.NoError(t, err)
	sp, err := s.Add(t.Context(), WithPrefixf("job"))
	assert.NoError(t, err)
	sp.Update("working")
	sp.Fail(errors.New("lost"))
	assert.NoError(t, sp.Close())
	s.Close()
	assert.Equal(t, ""+
		"level=INFO msg=\"spinner started\" spinner=0 prefix=job status=started\n"+
		"level=INFO msg=working spinner=0 prefix=job status=running\n"+
		"level=ERROR msg=lost spinner=0 prefix=job status=failed\n", logs.String())
	assert.Equal(t, 0, out.Len())
}

func TestSpinnerOneLine(t *testing.T) {
	got := new(Spinners).oneLine(text("a\r\nb\tc\x1b[2Kd\x1b[31mred\x1b[0m\xff\x07"))
	assert.Equal(t, "a  b cd\x1b[31mred\x1b[0m�", got)
}

func TestSpinnersStayLegacyWhenOff(t *testing.T) {
	th := asciiTheme(false)
	th.Spinner.Frames = []string{"[a]"}
	useTheme(t, th)
	themeRendering.Store(false)
	s, cio, tick := spinnersForTest(t)
	defer s.Close()
	sp, err := s.Add(t.Context())
	assert.NoError(t, err)
	sp.Update("x")
	tick()
	assert.Equal(t, "\r... x\n\r", <-cio.Out)
}
