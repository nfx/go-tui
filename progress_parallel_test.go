// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/theme"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestWithWorkersRejectsNonPositive(t *testing.T) {
	p := newProgressbar(t.Context())
	err := WithWorkers(0)(p)
	assert.ErrorIs(t, err, ErrInvalidState)
}

func TestWithWorkersSetsConfiguredValue(t *testing.T) {
	p := newProgressbar(t.Context())
	err := WithWorkers(7)(p)
	assert.NoError(t, err)
	assert.Equal(t, 7, p.workers)
}

func TestNewParallelProgressBarRunsConcurrently(t *testing.T) {
	tests := []struct {
		name    string
		items   []int
		workers int
	}{
		{name: "three workers", items: []int{1, 2, 3, 4, 5, 6}, workers: 3},
		{name: "no tty", items: []int{1, 2, 3}, workers: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var active, maxActive int64
			ready := make(chan struct{})
			var once sync.Once
			err := NewParallelProgressBar(t.Context(), "parallel", tt.items, func(v int) error {
				curr := atomic.AddInt64(&active, 1)
				for {
					prev := atomic.LoadInt64(&maxActive)
					if curr <= prev || atomic.CompareAndSwapInt64(&maxActive, prev, curr) {
						break
					}
				}
				if curr >= int64(tt.workers) {
					once.Do(func() {
						close(ready)
					})
				}
				select {
				case <-ready:
				case <-time.After(time.Second):
					return errors.New("timeout waiting for parallel start")
				}
				atomic.AddInt64(&active, -1)
				return nil
			}, WithWorkers(tt.workers), option.OpT(func(pb *Progressbar) error {
				pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
					return nil, ErrNoTTY
				}
				return nil
			}))
			assert.NoError(t, err)
			assert.True(t, maxActive > 1)
		})
	}
}

func TestNewParallelProgressBarStopsOnParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	var once sync.Once
	var ran atomic.Int64
	go func() {
		<-started
		cancel()
	}()
	err := NewParallelProgressBar(ctx, "parallel", make([]int, 100), func(int) error {
		ran.Add(1)
		once.Do(func() { close(started) })
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
		return nil
	}, WithWorkers(2), option.OpT(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, ran.Load() < 100)
}

func TestNewParallelProgressBarUsesDefaultNumCPU(t *testing.T) {
	orig := runtimeNumCPU
	runtimeNumCPU = func() int { return 4 }
	t.Cleanup(func() {
		runtimeNumCPU = orig
	})
	p := newProgressbar(t.Context())
	assert.Equal(t, 4, p.workers)
	items := []int{1, 2, 3, 4}
	var active int64
	var maxActive int64
	ready := make(chan struct{})
	var once sync.Once
	err := NewParallelProgressBar(t.Context(), "parallel", items, func(v int) error {
		curr := atomic.AddInt64(&active, 1)
		for {
			prev := atomic.LoadInt64(&maxActive)
			if curr <= prev || atomic.CompareAndSwapInt64(&maxActive, prev, curr) {
				break
			}
		}
		if curr >= 4 {
			once.Do(func() {
				close(ready)
			})
		}
		select {
		case <-ready:
		case <-time.After(time.Second):
			return errors.New("timeout waiting for parallel start")
		}
		atomic.AddInt64(&active, -1)
		return nil
	}, option.OpT(func(pb *Progressbar) error {
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	assert.NoError(t, err)
	assert.Equal(t, int64(4), maxActive)
}

func TestNewParallelProgressBarFailFastOnFirstError(t *testing.T) {
	items := make([]int, 64)
	for i := range items {
		items[i] = i
	}
	fail := errors.New("fail")
	var processed int64
	err := NewParallelProgressBar(t.Context(), "parallel", items, func(v int) error {
		atomic.AddInt64(&processed, 1)
		if v == 7 {
			return fail
		}
		time.Sleep(10 * time.Millisecond)
		return nil
	}, WithWorkers(4), option.OpT(func(pb *Progressbar) error {
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	assert.ErrorIs(t, err, fail)
	assert.True(t, processed < int64(len(items)))
}

func TestNewParallelProgressBarRecoversPanicsAsBug(t *testing.T) {
	err := NewParallelProgressBar(t.Context(), "parallel", []int{1, 2, 3}, func(v int) error {
		if v == 2 {
			panic("boom")
		}
		return nil
	}, WithWorkers(2), option.OpT(func(pb *Progressbar) error {
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))

	assert.ErrorIs(t, err, ErrBug)
	assert.Contains(t, err.Error(), "panic: boom")
	var pe *panicError
	assert.True(t, errors.As(err, &pe))
}

func TestNewParallelProgressBarPropagatesCloseError(t *testing.T) {
	closeErr := io.EOF
	err := NewParallelProgressBar(t.Context(), "parallel", []int{1}, func(v int) error {
		return nil
	}, option.OpT(func(pb *Progressbar) error {
		pb.err = closeErr
		pb.ticks = make(chan time.Time)
		pb.ticker = time.NewTicker(time.Hour)
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return &termIO{
				in:      in,
				out:     &bytes.Buffer{},
				Width:   40,
				Height:  1,
				Restore: func() error { return nil },
			}, nil
		}
		return nil
	}))
	assert.ErrorIs(t, err, closeErr)
}

func TestThemedParallelProgressCompletes(t *testing.T) {
	useTheme(t, asciiTheme(false))
	started := make(chan int, 4)
	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})}
	r := runParallel(t.Context(), t, []int{0, 1, 2, 3}, func(v int) error {
		started <- v
		return openedWithin(gates[v])
	}, WithWorkers(2))
	receiveN(t, started, 2)
	close(gates[0])
	close(gates[1])
	// a worker takes its next item only after adding the last one
	receiveN(t, started, 2)
	r.tick(time.Second)
	assert.Equal(t, frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"2 / 4 items | 0.00 items/s"), <-r.frames)
	close(gates[2])
	close(gates[3])
	out, err := r.wait(t)
	assert.NoError(t, err)
	// done: the rows are erased, and no completion line is retained
	assert.Equal(t, erased(t, 2), out)
	assert.True(t, r.restored.Load())
}

func TestThemedParallelProgressSharesOneRow(t *testing.T) {
	th := asciiTheme(false)
	th.Symbols.BarEmpty = "_"
	useTheme(t, th)
	var active, maxActive atomic.Int64
	all := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	r := runParallel(t.Context(), t, []int{0, 1, 2, 3, 4, 5}, func(int) error {
		n := active.Add(1)
		for prev := maxActive.Load(); n > prev && !maxActive.CompareAndSwap(prev, n); prev = maxActive.Load() {
		}
		if n == 3 {
			once.Do(func() { close(all) })
		}
		err := openedWithin(gate)
		active.Add(-1)
		return err
	}, WithWorkers(3))
	receiveN(t, all, 1)
	// a theme installed while the workers run does not reach the bar
	later := asciiTheme(false)
	later.Symbols.BarEmpty = "."
	assert.NoError(t, defaultIO.installTheme(later))
	r.tick(time.Second)
	// three workers, one bar in the theme captured at the start
	assert.Equal(t, frameOf("build  0%   "+strings.Repeat("_", 27), "0 / 6 items | 0.00 items/s"), <-r.frames)
	close(gate)
	out, err := r.wait(t)
	assert.NoError(t, err)
	assert.Equal(t, erased(t, 2), out)
	assert.Equal(t, int64(3), maxActive.Load())
}

func TestThemedParallelProgressFirstFailure(t *testing.T) {
	useTheme(t, asciiTheme(false))
	fail := errors.New("fail")
	started := make(chan int, 3)
	gate0, gate1 := make(chan struct{}), make(chan struct{})
	r := runParallel(t.Context(), t, []int{0, 1, 2}, func(v int) error {
		switch v {
		case 0:
			started <- v
			return openedWithin(gate0)
		case 1:
			started <- v
			if err := openedWithin(gate1); err != nil {
				return err
			}
			return fail
		}
		return nil
	}, WithWorkers(2))
	receiveN(t, started, 2)
	r.tick(time.Second)
	assert.Equal(t, frameOf("build  0%   "+strings.Repeat("-", 27), "0 / 3 items | 0.00 items/s"), <-r.frames)
	close(gate1)
	close(gate0)
	out, err := r.wait(t)
	// the failure alone, and the rows are erased rather than completed
	assert.ErrorIs(t, err, fail)
	assert.Equal(t, "fail", err.Error())
	assert.Equal(t, erased(t, 2), out)
	assert.True(t, r.restored.Load())
}

func TestThemedParallelProgressCancel(t *testing.T) {
	useTheme(t, asciiTheme(false))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan int, 2)
	gate := make(chan struct{})
	r := runParallel(ctx, t, []int{0, 1}, func(v int) error {
		started <- v
		return openedWithin(gate)
	}, WithWorkers(1))
	receiveN(t, started, 1)
	r.tick(time.Second)
	assert.Equal(t, frameOf("build  0%   "+strings.Repeat("-", 27), "0 / 2 items | 0.00 items/s"), <-r.frames)
	cancel()
	close(gate)
	out, err := r.wait(t)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, erased(t, 2), out)
	assert.True(t, r.restored.Load())
}

func TestThemedParallelProgressRenderError(t *testing.T) {
	th := theme.New()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Progress.Label = `{{ if eq .Current 1 }}{{ boom }}{{ end }}{{ .Label }}`
	useTheme(t, th)
	items := make([]int, 64)
	for i := range items {
		items[i] = i
	}
	var processed atomic.Int64
	second := make(chan struct{})
	gate := make(chan struct{})
	r := runParallel(t.Context(), t, items, func(v int) error {
		processed.Add(1)
		if v == 1 {
			close(second)
			return openedWithin(gate)
		}
		return nil
	}, WithWorkers(1))
	// the only worker added the first item before taking the second
	receiveN(t, second, 1)
	r.tick(time.Second)
	close(gate)
	out, err := r.wait(t)
	// nothing was drawn, so nothing is cleared
	assert.Equal(t, "", out)
	assert.True(t, r.restored.Load())
	// why rendering ended, not that it cancelled the workers
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redraw: theme.Templates.Progress.Label")
	assert.Contains(t, err.Error(), "boom")
	assert.True(t, !errors.Is(err, context.Canceled))
	assert.True(t, processed.Load() < int64(len(items)))
}
