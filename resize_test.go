// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestTermIORefreshSizeDirectTTY(t *testing.T) {
	origGet := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = origGet })
	tio := &termIO{
		Width:  80,
		Height: 24,
		fd:     2, // stderr
	}
	tio.refreshSize()
	assert.Equal(t, 120, tio.Width)
	assert.Equal(t, 40, tio.Height)
}

func TestTermIORefreshSizeNoFd(t *testing.T) {
	tio := &termIO{Width: 80, Height: 24, fd: 0}
	tio.refreshSize()
	// should not change when fd < 1
	assert.Equal(t, 80, tio.Width)
	assert.Equal(t, 24, tio.Height)
}

func TestTermIORefreshSizeWithChanIO(t *testing.T) {
	origGet := termGetSize
	termGetSize = func(int) (int, int, error) { return 100, 50, nil }
	t.Cleanup(func() { termGetSize = origGet })
	cio := &chanIO{fd: 2, width: 80, height: 24}
	tio := &termIO{Width: 80, Height: 24, cio: cio}
	tio.refreshSize()
	assert.Equal(t, 100, tio.Width)
	assert.Equal(t, 50, tio.Height)
}

func TestTermIORefreshSizeChanIONoFd(t *testing.T) {
	cio := &chanIO{fd: 0}
	tio := &termIO{Width: 80, Height: 24, cio: cio}
	tio.refreshSize()
	assert.Equal(t, 80, tio.Width)
}

func TestViewportWriteWithWidthUpdatesWidth(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	notify := make(chan viewportChanged, 10)
	vp := initViewport(ctx, notify, 40, 10)
	// write content at new width
	_, err := vp.writeWithWidth([]byte("hello\n"), 80)
	assert.NoError(t, err)
	<-notify
	// verify the viewport width was updated
	var buf bytes.Buffer
	_, err = vp.WriteTo(&buf)
	assert.NoError(t, err)
	// the line should be padded to the new width (80)
	assert.True(t, len(buf.Bytes()) > 40)
}

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
	p, err := newStartedProgressBar("downloading files", 20,
		WithInput(cio),
		WithOutput(cio),
		progressbarOpt(func(pb *Progressbar) error {
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
	assert.True(t, width(contentLine) <= 19)
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
	p, err := newStartedProgressBar("downloading files", 10,
		WithInput(cio),
		WithOutput(cio),
		progressbarOpt(func(pb *Progressbar) error {
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
	t.Cleanup(func() { _ = p.Close() })

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

func TestChanIOClearsWrappedManagedRowsAfterWidthShrink(t *testing.T) {
	cio, stdout := chainIOforTest(t, 40, 6)
	vp, err := cio.pushViewport()
	assert.NoError(t, err)

	const wideLine = "123456789012345678901234567890123456789"
	_, err = vp.writeWithWidth([]byte(wideLine+"\n\r"), 40)
	assert.NoError(t, err)
	_ = waitOutputContains(t, stdout.C, wideLine)

	cio.width = 10
	_, err = vp.writeWithWidth([]byte("short\n\r"), 10)
	assert.NoError(t, err)
	out := waitOutputContains(t, stdout.C, "short")

	assert.Equal(t, 4, strings.Count(out, "\x1b[K"))
	assert.Equal(t, 3, strings.Count(out, "\x1b[1A"))
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
			p := &Progressbar{
				progressState: progressState{
					maxNum:     10,
					currentNum: 5,
					redrawAt:   time.Now(),
					startedAt:  time.Now().Add(-time.Second),
				},
				label: "test",
				io: &termIO{
					out:     &bytes.Buffer{},
					Width:   tt.width,
					Height:  1,
					Restore: func() error { return nil },
				},
				now: func() time.Time { return time.Now() },
			}
			frame := &bytes.Buffer{}
			// must not panic
			p.tick(frame)
		})
	}
}

func TestProgressbarLabelTruncation(t *testing.T) {
	p := &Progressbar{
		progressState: progressState{
			maxNum:     10,
			currentNum: 5,
			redrawAt:   time.Now(),
			startedAt:  time.Now().Add(-time.Second),
		},
		label: "this is a very long label that exceeds terminal width",
		io: &termIO{
			out:     &bytes.Buffer{},
			Width:   15,
			Height:  1,
			Restore: func() error { return nil },
		},
		now: func() time.Time { return time.Now() },
	}
	frame := &bytes.Buffer{}
	p.tick(frame)
	// output must not exceed width - 1 = 14
	out, ok := p.io.out.(*bytes.Buffer)
	assert.True(t, ok)
	lines := bytes.Split(out.Bytes(), []byte("\n"))
	for _, line := range lines {
		w := width(line)
		assert.True(t, w <= 14)
	}
}

func TestSpinnerRedrawTruncatesLongMessage(t *testing.T) {
	s := newSpinners()
	s.io = &termIO{
		out:     &bytes.Buffer{},
		Width:   20,
		Height:  4,
		Restore: func() error { return nil },
	}
	s.state = []*spinnerState{
		{
			frames:  []string{"..."},
			Message: "this is a very long spinner message that should be truncated",
			tick:    0,
		},
	}
	s.displayed = 1
	prevActive := s.redraw(0)
	assert.Equal(t, 1, prevActive)
	out, ok := s.io.out.(*bytes.Buffer)
	assert.True(t, ok)
	lines := bytes.Split(out.Bytes(), []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		// each line must fit within the full width = 20
		assert.True(t, width(line) <= 20)
	}
}

func TestDropdownClampDisplayShrink(t *testing.T) {
	d := &dropdown{
		relevant:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		displayed: []int{0, 1, 2, 3, 4},
		offset:    0,
		selected:  4,
	}
	// shrink to height 4 → capacity = 2
	d.clampDisplay(4)
	assert.Equal(t, 2, len(d.displayed))
	assert.True(t, d.selected < len(d.displayed))
}

func TestDropdownClampDisplayGrow(t *testing.T) {
	d := &dropdown{
		relevant:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		displayed: []int{0, 1},
		offset:    0,
		selected:  1,
	}
	// grow to height 20 → capacity = 10
	d.clampDisplay(20)
	assert.Equal(t, 10, len(d.displayed))
	assert.Equal(t, 1, d.selected)
}

func TestDropdownClampDisplayClampsOffset(t *testing.T) {
	d := &dropdown{
		relevant:  []int{0, 1, 2, 3, 4},
		displayed: []int{3, 4},
		offset:    3,
		selected:  1,
	}
	// height 6 → capacity = 3, offset 3 + 3 > 5 → clamp offset
	d.clampDisplay(6)
	assert.True(t, d.offset+len(d.displayed) <= len(d.relevant))
}

func TestMultichoiceClampDisplayShrink(t *testing.T) {
	m := &multichoice{
		relevant:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		displayed: []int{0, 1, 2, 3, 4},
		offset:    0,
		active:    4,
	}
	m.clampDisplay(4)
	assert.Equal(t, 2, len(m.displayed))
	assert.True(t, m.active < len(m.displayed))
}

func TestInputVisibleWindowNoClipping(t *testing.T) {
	i := &input{cursor: 3}
	runes := []rune("hello")
	start, end := i.visibleWindow(runes, 10)
	assert.Equal(t, 0, start)
	assert.Equal(t, 5, end)
}

func TestInputVisibleWindowClipsCursorAtStart(t *testing.T) {
	i := &input{cursor: 2}
	runes := []rune("abcdefghij")
	start, end := i.visibleWindow(runes, 5)
	assert.Equal(t, 0, start)
	assert.Equal(t, 5, end)
}

func TestInputVisibleWindowClipsCursorAtEnd(t *testing.T) {
	i := &input{cursor: 9}
	runes := []rune("abcdefghij")
	start, end := i.visibleWindow(runes, 5)
	assert.Equal(t, 5, start)
	assert.Equal(t, 10, end)
}

func TestInputVisibleWindowClipsCursorInMiddle(t *testing.T) {
	i := &input{cursor: 5}
	runes := []rune("abcdefghij")
	start, end := i.visibleWindow(runes, 4)
	assert.Equal(t, 3, start)
	assert.Equal(t, 7, end)
}

func TestInputVisibleWindowZeroAvail(t *testing.T) {
	i := &input{cursor: 3}
	runes := []rune("hello")
	start, end := i.visibleWindow(runes, 0)
	assert.Equal(t, 0, start)
	assert.Equal(t, 5, end)
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
