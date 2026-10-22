// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"reflect"
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
