// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"
)

// opt configures a widget. Every option supports a fixed set of widgets and
// returns an error wrapping [ErrWrongWidget] when applied to any other.
//
// Widget entry points reject options they do not support, so a misplaced
// option fails loudly instead of doing nothing. Use [WithOptions] to share
// one set of options between different widgets.
type opt func(any) error

type opts []opt

// Apply applies every option to target and stops at the first error,
// including [ErrWrongWidget].
func (o opts) Apply(target any) error {
	for _, o := range o {
		err := o(target)
		if err != nil {
			return err
		}
	}
	return nil
}

// WithOptions bundles options for reuse across widgets. Each member is applied
// when the widget supports it and skipped otherwise. The bundle fails with
// [ErrWrongWidget] only when the widget supports none of its members. Any
// other error from a member, such as an invalid value, is returned as is.
func WithOptions(o ...opt) opt {
	return func(target any) error {
		if len(o) == 0 {
			return nil
		}
		applied := false
		for _, o := range o {
			err := o(target)
			if errors.Is(err, ErrWrongWidget) {
				continue
			}
			if err != nil {
				return err
			}
			applied = true
		}
		if !applied {
			return fmt.Errorf("%w: no option in the bundle supports %T", ErrWrongWidget, target)
		}
		return nil
	}
}

// opT turns a setter for one target type into an option. T is either a widget
// pointer or an interface that several widgets implement.
func opT[T any](o func(T) error) opt {
	return func(raw any) error {
		target, ok := raw.(T)
		if !ok {
			return fmt.Errorf("%w: need %v, got %T", ErrWrongWidget, reflect.TypeFor[T](), raw)
		}
		return o(target)
	}
}

// applyToAny applies o to every target that supports it, for widgets that are
// assembled from several parts. It fails when none of the targets supports o.
func applyToAny(o opt, targets ...any) error {
	var wrong error
	applied := false
	for _, target := range targets {
		err := o(target)
		if errors.Is(err, ErrWrongWidget) {
			wrong = err
			continue
		}
		if err != nil {
			return err
		}
		applied = true
	}
	if !applied {
		return wrong
	}
	return nil
}

type withIO interface {
	setWriter(w io.Writer)
	setReader(r io.Reader)
}

// WithInput overrides the input source of an interactive widget.
func WithInput(r io.Reader) opt {
	return opT(func(x withIO) error {
		x.setReader(r)
		return nil
	})
}

// WithOutput overrides the output destination of an interactive widget.
func WithOutput(w io.Writer) opt {
	return opT(func(x withIO) error {
		x.setWriter(w)
		return nil
	})
}

// withPrompt is implemented by widgets that ask a question and print the answer.
type withPrompt interface {
	setHide()
	setLabelTemplate(tmpl string)
	setAnswerTemplate(tmpl string)
	setDefault(d string)
}

type withContext interface {
	setContext(ctx context.Context)
	getContext() context.Context
}

// WithContext sets the context of an interactive widget.
//
// design tradeoff - we're not passing context as the first argument, because we don't always need it.
func WithContext(ctx context.Context) opt {
	return opT(func(x withContext) error {
		x.setContext(ctx)
		return nil
	})
}

// WithTimeout wraps the current context of an interactive widget.
func WithTimeout(timeout time.Duration) opt {
	return opT(func(x withContext) error {
		ctx := x.getContext()
		// at the moment our interfaces don't care about cancellation,
		// but we may want to revisit this later.
		ctx, _ = context.WithTimeout(ctx, timeout) //nolint:govet // ...
		x.setContext(ctx)
		return nil
	})
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
