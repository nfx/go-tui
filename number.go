// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"github.com/nfx/go-tui/internal/option"
)

type numberType interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// Number prompts for a decimal number of explicit type T (excluding uintptr).
// Invalid or out-of-range input stays editable. Errors return T's zero value.
//
// Up/Down step integers by one and floats at the caret's decimal place.
// Steps preserve fractional digits and reject overflow or invisible changes.
//
// Only [WithDefault], [WithAtLeast], [WithAtMost], [WithInput] and [WithOutput] apply.
// Bounds are inclusive: typed values outside them are rejected on Enter, and
// Up/Down refuse to step past them.
func Number[T numberType](ctx context.Context, label string, option ...option.Opt) (T, error) {
	n := newNumber[T](ctx, label)
	return n.read(ctx, option...)
}

// WithAtLeast rejects [Number] values below v. The bound may have any numeric
// type; it is compared exactly and, for floats, at the precision of the answer.
func WithAtLeast[B numberType](v B) option.Opt {
	return option.OpT(func(x withNumberBounds) error {
		b, err := newNumberBound(v)
		if err != nil {
			return err
		}
		x.setMin(b)
		return nil
	})
}

// WithAtMost rejects [Number] values above v. See [WithAtLeast].
func WithAtMost[B numberType](v B) option.Opt {
	return option.OpT(func(x withNumberBounds) error {
		b, err := newNumberBound(v)
		if err != nil {
			return err
		}
		x.setMax(b)
		return nil
	})
}
