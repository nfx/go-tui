// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

type opt func(any) error

type opts []opt

func (o opts) Apply(d any) error {
	var err error
	for _, o := range o {
		err = o(d)
		if errors.Is(err, ErrWrongWidget) {
			continue
		} else if err != nil {
			return err
		}
	}
	return nil
}

func WithOptions(o ...opt) opt {
	return func(a any) error {
		return opts(o).Apply(a)
	}
}

type withIO interface {
	setWriter(w io.Writer)
	setReader(r io.Reader)
}

func WithInput(r io.Reader) opt {
	return func(d any) error {
		io, ok := d.(withIO)
		if !ok {
			return fmt.Errorf("%w: cannot set IO", ErrInvalidState)
		}
		io.setReader(r)
		return nil
	}
}

func WithOutput(w io.Writer) opt {
	return func(d any) error {
		io, ok := d.(withIO)
		if !ok {
			return fmt.Errorf("%w: cannot set IO", ErrInvalidState)
		}
		io.setWriter(w)
		return nil
	}
}

type withContext interface {
	setContext(ctx context.Context)
	getContext() context.Context
}

// design tradeoff - we're not passing context as the first argument, because we don't always need it.
func WithContext(ctx context.Context) opt {
	return func(d any) error {
		x, ok := d.(withContext)
		if !ok {
			return fmt.Errorf("%w: cannot set context", ErrInvalidState)
		}
		x.setContext(ctx)
		return nil
	}
}

func WithTimeout(timeout time.Duration) opt {
	return func(d any) error {
		x, ok := d.(withContext)
		if !ok {
			return fmt.Errorf("%w: cannot set context", ErrInvalidState)
		}
		ctx := x.getContext()
		// at the moment our interfaces don't care about cancellation,
		// but we may want to revisit this later.
		ctx, _ = context.WithTimeout(ctx, timeout) //nolint:govet // ...
		x.setContext(ctx)
		return nil
	}
}

type config struct {
	ctx context.Context // TODO: wrap with context.WithCancelClause
	in  io.Reader
	out io.Writer
}

// implement [withContext] interface.
func (c *config) setContext(ctx context.Context) {
	c.ctx = ctx
}

// implement [withContext] interface.
func (c *config) getContext() context.Context {
	return c.ctx
}

// implement [withIO] interface.
func (c *config) setReader(r io.Reader) {
	c.in = r
}

// implement [withIO] interface.
func (c *config) setWriter(w io.Writer) {
	c.out = w
}
