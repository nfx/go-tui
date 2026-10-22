// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"io"
	"iter"
)

func WithFormatRate(f func(float64) string) opt {
	return opT(func(p *Progressbar) error {
		p.fmtRate = f
		return nil
	})
}

// WithWorkers configures the amount of parallel workers for NewParallelProgressBar.
func WithWorkers(workers int) opt {
	return opT(func(p *Progressbar) error {
		if workers <= 0 {
			return fmt.Errorf("%w: workers must be greater than 0", ErrInvalidState)
		}
		p.workers = workers
		return nil
	})
}

// NewMaxProgressBar returns progress bar towards the max number.
func NewMaxProgressBar(ctx context.Context, label string, size int64, opts ...opt) (*Progressbar, error) {
	return newStartedProgressBar(ctx, label, size, opts...)
}

// NewSliceProgressBar returns progress bar that updates as long as iterator consumed.
func NewSliceProgressBar[T any](ctx context.Context, label string, slice []T, opts ...opt) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		p, err := newStartedProgressBar(ctx, label, int64(len(slice)), opts...)
		if err != nil {
			yield(zero, err)
			return
		}
		yieldable := true
		defer func() {
			err = p.Close()
			if err != nil && yieldable {
				yield(zero, err)
			}
		}()
		for _, v := range slice {
			if !yield(v, nil) {
				yieldable = false
				return
			}
			p.Add(1)
		}
	}
}

// NewParallelProgressBar executes yield in parallel while updating progress.
func NewParallelProgressBar[T any](
	ctx context.Context,
	label string,
	slice []T,
	yield func(T) error,
	opts ...opt,
) error {
	// one bar, theme and binding for all workers; they only add to it
	p, err := newStartedProgressBar(ctx, label, int64(len(slice)), opts...)
	if err != nil {
		return err
	}
	if len(slice) == 0 {
		return p.Close()
	}
	ctx, cancel := context.WithCancelCause(p.ctx)
	runner := &parallelProgressRunner[T]{
		ctx:    ctx,
		cancel: cancel,
		p:      p,
		slice:  slice,
		yield:  yield,
		jobs:   make(chan int),
	}
	runErr := runner.run()
	closeErr := p.Close()
	if runner.firstErr == nil && closeErr != nil {
		// workers were only cancelled because rendering failed
		runErr = nil
	}
	err = errors.Join(runErr, closeErr)
	cancel(err)
	return err
}

func NewFileProgressReader(ctx context.Context, r io.Reader, label string, o ...opt) (*wrapReader, error) {
	p := newProgressbar(ctx)
	started := false
	defer func() {
		if !started {
			p.cancel(nil)
			p.ticker.Stop()
		}
	}()
	err := opts(o).Apply(p)
	if err != nil {
		p.cancel(err)
		return nil, fmt.Errorf("apply option: %w", err)
	}
	wrap := &wrapReader{r: r, p: p}
	size, err := wrap.Size()
	if err != nil {
		return nil, fmt.Errorf("size: %w", err)
	}
	err = p.bindLook(ctx)
	if err != nil {
		return nil, err
	}
	p.bytes = true
	p.showRate = true
	p.showEstimate = true
	p.maxNum = size
	p.label = ansi.Text(label)
	p.io, err = p.makeTermIO(p.in, p.out)
	if errors.Is(err, ErrNoTTY) {
		p.io = nil
		p.ticker.Stop()
		started = true
		return wrap, nil
	}
	if err != nil {
		return nil, fmt.Errorf("make io: %w", err)
	}
	err = p.io.restoreMode()
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	p.startedAt = p.now()
	p.redrawAt = p.startedAt
	p.emit(progressInit{
		Label: p.label.String(),
		Max:   p.maxNum,
	})
	started = true
	go p.start(p.ctx) //nolint:contextcheck // p.ctx derives from ctx and is canceled by Close
	return wrap, nil
}
