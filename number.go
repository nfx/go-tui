// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/prompt"
)

type numberType = prompt.Numeric

// Number prompts for a decimal number of explicit type T (excluding uintptr).
// Invalid or out-of-range input stays editable. Errors return T's zero value.
//
// Up/Down step integers by one and floats at the caret's decimal place.
// Steps preserve fractional digits and reject overflow or invisible changes.
//
// Only [WithDefault], [WithAtLeast], [WithAtMost], [WithInput] and [WithOutput] apply.
// Bounds are inclusive: typed values outside them are rejected on Enter, and
// Up/Down refuse to step past them.
func Number[T numberType](ctx context.Context, label string, opts ...option.Opt) (T, error) {
	return prompt.Number[T](ctx, label, opts...)
}

// WithAtLeast rejects [Number] values below v. The bound may have any numeric
// type; it is compared exactly and, for floats, at the precision of the answer.
func WithAtLeast[B numberType](v B) option.Opt { return prompt.WithAtLeast(v) }

// WithAtMost rejects [Number] values above v. See [WithAtLeast].
func WithAtMost[B numberType](v B) option.Opt { return prompt.WithAtMost(v) }
