// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/progress"
	"io"
	"iter"
)

func WithFormatRate(f func(float64) string) option.Opt {
	return progress.WithFormatRate(f)
}

// WithWorkers configures the amount of parallel workers for NewParallelProgressBar.
func WithWorkers(workers int) option.Opt {
	return progress.WithWorkers(workers)
}

// NewMaxProgressBar returns progress bar towards the max number.
func NewMaxProgressBar(ctx context.Context, label string, size int64, opts ...option.Opt) (*Progressbar, error) {
	return progress.NewMaxProgressBar(ctx, label, size, opts...)
}

// NewSliceProgressBar returns progress bar that updates as long as iterator consumed.
func NewSliceProgressBar[T any](ctx context.Context, label string, slice []T, opts ...option.Opt) iter.Seq2[T, error] {
	return progress.NewSliceProgressBar[T](ctx, label, slice, opts...)
}

// NewParallelProgressBar executes yield in parallel while updating progress.
func NewParallelProgressBar[T any](
	ctx context.Context,
	label string,
	slice []T,
	yield func(T) error,
	opts ...option.Opt,
) error {
	return progress.NewParallelProgressBar[T](ctx, label, slice, yield, opts...)
}

func NewFileProgressReader(ctx context.Context, r io.Reader, label string, o ...option.Opt) (*wrapReader, error) {
	return progress.NewFileProgressReader(ctx, r, label, o...)
}

// Progressbar shows the progress of an operation in the terminal.
type Progressbar = progress.Progressbar

type wrapReader = progress.WrapReader
