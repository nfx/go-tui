// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"context"

	"github.com/nfx/go-tui/internal/option"
)

type Numeric interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// Number prompts for a decimal number of type T.
func Number[T Numeric](ctx context.Context, label string, opts ...option.Opt) (T, error) {
	n := newNumber[T](ctx, label)
	return n.read(ctx, opts...)
}

// WithAtLeast rejects values below v.
func WithAtLeast[B Numeric](v B) option.Opt {
	return option.OpT(func(x withNumberBounds) error {
		b, err := newNumberBound(v)
		if err != nil {
			return err
		}
		x.setMin(b)
		return nil
	})
}

// WithAtMost rejects values above v.
func WithAtMost[B Numeric](v B) option.Opt {
	return option.OpT(func(x withNumberBounds) error {
		b, err := newNumberBound(v)
		if err != nil {
			return err
		}
		x.setMax(b)
		return nil
	})
}
