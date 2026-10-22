// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"iter"

	"github.com/nfx/go-tui/internal/choice"
	"github.com/nfx/go-tui/internal/option"
)

// Multichoice returns checked items in input order. Enter confirms without
// checking the focused item; a successful empty selection is a non-nil slice.
// Items are shallowly copied. Use one active prompt per shared input reader.
func Multichoice[T any](ctx context.Context, label string, items []T, o ...option.Opt) ([]T, error) {
	return choice.Multichoice[T](ctx, label, items, o...)
}

// MultichoiceKVK snapshots entries sorted by fmt.Sprint(key) and returns keys.
// Equal formatted keys have unspecified relative order. Templates receive
// entries with Key and Value, including every element of the answer collection.
func MultichoiceKVK[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	o ...option.Opt,
) ([]K, error) {
	return choice.MultichoiceKVK[K, V](ctx, label, items, o...)
}

// MultichoiceKVV returns values in the same entry order as MultichoiceKVK.
// Duplicate and non-comparable values remain independently selectable.
func MultichoiceKVV[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	o ...option.Opt,
) ([]V, error) {
	return choice.MultichoiceKVV[K, V](ctx, label, items, o...)
}

// MultichoiceLazy displays a finite sequence progressively and enables Enter
// only after successful exhaustion. Results keep producer arrival order.
// Publication stops on cancellation; iterator work blocked outside yield must
// observe a context supplied by its caller to be interruptible.
func MultichoiceLazy[T any](
	ctx context.Context,
	label string,
	items iter.Seq2[T, error],
	o ...option.Opt,
) ([]T, error) {
	return choice.MultichoiceLazy[T](ctx, label, items, o...)
}
