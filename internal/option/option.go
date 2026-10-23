// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package option

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/errs"
	"reflect"
)

// Opt configures a widget. Every option supports a fixed set of widgets and
// returns an error wrapping [errs.ErrWrongWidget] when applied to any other.
//
// Widget entry points reject options they do not support, so a misplaced
// option fails loudly instead of doing nothing.
type Opt func(any) error

type Opts []Opt

// Apply applies every option to target and stops at the first error,
// including [errs.ErrWrongWidget].
func (o Opts) Apply(target any) error {
	for _, o := range o {
		err := o(target)
		if err != nil {
			return err
		}
	}
	return nil
}

// OpT turns a setter for one target type into an option. T is either a widget
// pointer or an interface that several widgets implement.
func OpT[T any](o func(T) error) Opt {
	return func(raw any) error {
		target, ok := raw.(T)
		if !ok {
			return fmt.Errorf("%w: need %v, got %T", errs.ErrWrongWidget, reflect.TypeFor[T](), raw)
		}
		return o(target)
	}
}

// ApplyToAny applies o to every target that supports it, for widgets that are
// assembled from several parts. It fails when none of the targets supports o.
func ApplyToAny(o Opt, targets ...any) error {
	var wrong error
	applied := false
	for _, target := range targets {
		err := o(target)
		if errors.Is(err, errs.ErrWrongWidget) {
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
