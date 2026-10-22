// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"iter"

	"github.com/nfx/go-tui/internal/choice"
	"github.com/nfx/go-tui/internal/option"
)

func DropdownKV[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	opts ...option.Opt,
) (K, V, error) {
	return choice.DropdownKV[K, V](ctx, label, items, opts...)
}

func Dropdown[T any](ctx context.Context, label string, items []T, opts ...option.Opt) (T, error) {
	return choice.Dropdown[T](ctx, label, items, opts...)
}

func DropdownLazy[V any](ctx context.Context, label string, itemFn iter.Seq2[V, error], o ...option.Opt) (V, error) {
	return choice.DropdownLazy[V](ctx, label, itemFn, o...)
}

func DropdownIndex(ctx context.Context, label string, items []any, o ...option.Opt) (int, error) {
	return choice.DropdownIndex(ctx, label, items, o...)
}

// WithHide skips printing the answer of a dropdown or an input.
func WithHide() option.Opt {
	return option.WithHide()
}
