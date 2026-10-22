// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"github.com/nfx/go-tui/internal/ansi"
	"io"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestProgressbarTickShrinksBetweenTicks(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := start
	p, err := newStartedProgressBar(t.Context(), "downloading files", 20,
		WithInput(cio),
		WithOutput(cio),
		OpT(func(pb *Progressbar) error {
			pb.now = func() time.Time { return now }
			pb.redrawAt = start
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   80,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, p.Close()) })
	p.Add(10)
	now = start.Add(time.Second)
	sendTickOrFatal(t, ticks, now)
	_ = receiveOutputOrFatal(t, cio.Out, "no progress output")
	// shrink terminal width between ticks
	p.io.Width = 20
	now = start.Add(2 * time.Second)
	sendTickOrFatal(t, ticks, now)
	output := receiveOutputOrFatal(t, cio.Out, "no progress output after shrink")
	// the frame ends with "\n\r"; the content starts after the last \r before it
	raw := []byte(output)
	body, ok := bytes.CutSuffix(raw, []byte("\n\r"))
	if !ok {
		t.Fatalf("frame missing trailing \\n\\r terminator: %q", output)
	}
	lo := bytes.LastIndex(body, []byte("\r"))
	if lo < 0 {
		t.Fatalf("frame missing leading \\r before content: %q", output)
	}
	contentLine := body[lo+1:]
	// content must not exceed shrunken width minus 1 (autowrap column)
	assert.True(t, ansi.Text(contentLine).Width() <= 19)
}

func TestProgressbarTickOnResizeExitsWhenDone(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	resizeCh := make(chan struct{})
	p, err := newStartedProgressBar(t.Context(), "downloading files", 10,
		WithInput(cio),
		WithOutput(cio),
		OpT(func(pb *Progressbar) error {
			pb.ticks = make(chan time.Time)
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:       in,
					out:      out,
					Width:    80,
					Height:   1,
					Restore:  func() error { return nil },
					onResize: resizeCh,
				}, nil
			}
			return nil
		}),
	)
	assert.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck // test cleanup

	p.Add(10)

	select {
	case resizeCh <- struct{}{}:
	case <-time.After(time.Second):
		t.Fatal("failed to send resize event")
	}

	select {
	case <-p.stopped:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected progressbar to stop after onResize when done")
	}
}

func TestProgressbarTickTinyWidth(t *testing.T) {
	tests := []struct {
		name  string
		width int
	}{
		{"zero", 0},
		{"one", 1},
		{"two", 2},
		{"three", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := withLook(t, &Progressbar{
				progressState: progressState{
					maxNum:     10,
					currentNum: 5,
					redrawAt:   time.Now(),
					startedAt:  time.Now().Add(-time.Second),
				},
				label: ansi.Text("test"),
				io: &termIO{
					out:     &bytes.Buffer{},
					Width:   tt.width,
					Height:  1,
					Restore: func() error { return nil },
				},
				now: func() time.Time { return time.Now() },
			})
			frame := &bytes.Buffer{}
			// must not panic
			p.tick(frame)
		})
	}
}

func TestProgressbarLabelTruncation(t *testing.T) {
	p := withLook(t, &Progressbar{
		progressState: progressState{
			maxNum:     10,
			currentNum: 5,
			redrawAt:   time.Now(),
			startedAt:  time.Now().Add(-time.Second),
		},
		label: ansi.Text("this is a very long label that exceeds terminal width"),
		io: &termIO{
			out:     &bytes.Buffer{},
			Width:   15,
			Height:  1,
			Restore: func() error { return nil },
		},
		now: func() time.Time { return time.Now() },
	})
	frame := &bytes.Buffer{}
	p.tick(frame)
	// output must not exceed width - 1 = 14
	out, ok := p.io.out.(*bytes.Buffer)
	assert.True(t, ok)
	lines := bytes.Split(out.Bytes(), []byte("\n"))
	for _, line := range lines {
		w := ansi.Text(line).Width()
		assert.True(t, w <= 14)
	}
}

func sendTickOrFatal(t *testing.T, ticks chan time.Time, now time.Time) {
	t.Helper()
	select {
	case ticks <- now:
	case <-time.After(time.Second):
		t.Fatalf("tick not delivered")
	}
}

func receiveOutputOrFatal(t *testing.T, out <-chan string, msg string) string {
	t.Helper()
	select {
	case output := <-out:
		return output
	case <-time.After(time.Second):
		t.Fatalf("%s", msg)
		return ""
	}
}
