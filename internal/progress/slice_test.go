// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package progress

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestNewSliceProgressBar(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &term.ChanIO{
		Ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 2),
	}
	ticks := make(chan time.Time, 1)
	var seen *Progressbar

	items := []int{1, 2, 3}
	terst.UseIO(t, cio, cio)
	seq := NewSliceProgressBar(t.Context(), "items", items,
		option.OpT(func(pb *Progressbar) error {
			seen = pb
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.increments = make(chan int64, 8)
			pb.now = func() time.Time { return time.Now() }
			pb.redrawAt = pb.now()
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     out,
					Width:   30,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)

	var got []int
	for v, err := range seq {
		assert.NoError(t, err)
		got = append(got, v)
	}

	for {
		select {
		case n := <-seen.increments:
			seen.currentNum += n
		default:
			goto drainedSlice
		}
	}
drainedSlice:
	assert.Equal(t, items, got)
	assert.NotNil(t, seen)
	assert.Equal(t, int64(len(items)), seen.currentNum)
	assert.True(t, seen.isDone())
}

func TestNewSliceProgressBarStopsOnYieldFalse(t *testing.T) {
	seq := NewSliceProgressBar(t.Context(), "label", []int{1, 2, 3}, option.OpT(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			return nil, errs.ErrNoTTY
		}
		return nil
	}))
	count := 0
	seq(func(v int, err error) bool {
		count++
		return false
	})
	if count != 1 {
		t.Fatalf("expected one item, got %d", count)
	}
}

func TestNewSliceProgressBarReturnsError(t *testing.T) {
	seq := NewSliceProgressBar(t.Context(), "label", []int{1}, option.OpT(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			return nil, errors.New("boom")
		}
		return nil
	}))
	var gotErr error
	seq(func(v int, err error) bool {
		gotErr = err
		return false
	})
	if gotErr == nil {
		t.Fatalf("expected error")
	}
}

func TestThemedSliceProgress(t *testing.T) {
	terst.UseTheme(t, terst.ASCIITheme(false))
	frames := make(terst.FrameWriter, 16)
	ticks := make(chan time.Time)
	var clock atomic.Int64
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	items := []string{"a", "b", "c", "d"}
	seq := NewSliceProgressBar(t.Context(), "items", items, option.OpT(func(pb *Progressbar) error {
		pb.now = func() time.Time { return start.Add(time.Duration(clock.Load())) }
		pb.ticks = ticks
		pb.makeTermIO = func(in io.Reader, _ io.Writer) (*term.IO, error) {
			return &term.IO{In: in, Out: frames, Width: 30, Height: 10, Restore: func() error { return nil }}, nil
		}
		return nil
	}))
	var got []string
	for v, err := range seq {
		assert.NoError(t, err)
		got = append(got, v)
		if v == "c" {
			clock.Store(int64(time.Second))
			ticks <- time.Time{}
			// two items done; too few for a rate, so no estimate
			assert.Equal(t, frameOf("items  50%  ========>--------", "2 / 4 items | 0.00 items/s"), <-frames)
		}
	}
	assert.Equal(t, items, got)
	// the iterator closed the bar, which erased its rows
	close(frames)
	var rest strings.Builder
	for f := range frames {
		rest.WriteString(f)
	}
	assert.Equal(t, erased(t, 2), rest.String())
}

func TestThemedSliceProgressReturnsRenderError(t *testing.T) {
	th := theme.New()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Progress.Hint = `{{ boom }}`
	terst.UseTheme(t, th)
	ticks := make(chan time.Time)
	seq := NewSliceProgressBar(t.Context(), "items", []int{1, 2}, option.OpT(func(pb *Progressbar) error {
		pb.ticks = ticks
		pb.makeTermIO = func(in io.Reader, _ io.Writer) (*term.IO, error) {
			return &term.IO{In: in, Out: io.Discard, Width: 30, Height: 10, Restore: func() error { return nil }}, nil
		}
		return nil
	}))
	var errs []error
	for v, err := range seq {
		if v == 1 {
			ticks <- time.Time{}
		}
		errs = append(errs, err)
	}
	assert.Equal(t, 3, len(errs))
	assert.Contains(t, errs[2].Error(), "theme.Progress.Hint")
}
