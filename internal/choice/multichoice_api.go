// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"sort"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
)

// Multichoice returns checked items in input order.
func Multichoice[T any](ctx context.Context, label string, items []T, o ...option.Opt) ([]T, error) {
	return pickMultichoice(ctx, label, items, targetOf[T](), o)
}

// pickMultichoice runs a multichoice whose typed defaults have the type of target.
func pickMultichoice[T any](
	ctx context.Context,
	label string,
	items []T,
	target choiceTarget,
	o []option.Opt,
) ([]T, error) {
	if len(items) == 0 {
		return nil, errs.ErrNoItems
	}
	snapshot := slices.Clone(items)
	m := newMultichoice(ctx)
	m.target = target
	m.Label = label
	for _, item := range snapshot {
		m.Items = append(m.Items, item)
	}
	err := m.run(ctx, o...)
	if err != nil {
		return nil, err
	}
	result := make([]T, 0, m.count)
	for _, i := range m.indices() {
		result = append(result, snapshot[i])
	}
	return result, nil
}

// MultichoiceKVK snapshots entries sorted by fmt.Sprint(key) and returns keys.
func MultichoiceKVK[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	o ...option.Opt,
) ([]K, error) {
	target := choiceTarget{
		typ:  reflect.TypeFor[K](),
		proj: mapKey[K, V],
	}
	entries, err := multichoiceMap(ctx, label, items, target, o)
	if err != nil {
		return nil, err
	}
	result := make([]K, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Key)
	}
	return result, nil
}

// MultichoiceKVV returns values in the same entry order as MultichoiceKVK.
func MultichoiceKVV[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	o ...option.Opt,
) ([]V, error) {
	target := choiceTarget{
		typ:  reflect.TypeFor[V](),
		proj: mapValue[K, V],
	}
	entries, err := multichoiceMap(ctx, label, items, target, o)
	if err != nil {
		return nil, err
	}
	result := make([]V, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Value)
	}
	return result, nil
}

// multichoiceMap snapshots key/value pairs in display order before sharing selection logic.
func multichoiceMap[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	target choiceTarget,
	o []option.Opt,
) ([]mapKV[K, V], error) {
	entries := make([]mapKV[K, V], 0, len(items))
	for key, value := range items {
		entries = append(entries, mapKV[K, V]{key, value})
	}
	sort.SliceStable(entries, func(i, j int) bool { return fmt.Sprint(entries[i].Key) < fmt.Sprint(entries[j].Key) })
	// the theme presents the entries, projected to their keys
	return pickMultichoice(ctx, label, entries, target, append([]option.Opt{WithTemplate("(label .Key)")}, o...))
}

// MultichoiceLazy displays a finite sequence progressively and enables Enter only after successful exhaustion.
func MultichoiceLazy[T any](
	ctx context.Context,
	label string,
	items iter.Seq2[T, error],
	o ...option.Opt,
) ([]T, error) {
	if items == nil {
		return nil, fmt.Errorf("%w: MultichoiceLazy sequence is nil", errs.ErrInvalidOption)
	}
	m := newMultichoice(ctx)
	m.target = targetOf[T]()
	m.Label = label
	m.loading = true
	m.producer = func(yield func(any, error) bool) {
		for item, err := range items {
			if !yield(item, err) {
				return
			}
		}
	}
	err := m.run(ctx, o...)
	if err != nil {
		return nil, err
	}
	result := make([]T, 0, m.count)
	for _, i := range m.indices() {
		// The producer preserves T, including nil interface values.
		value, ok := m.Items[i].(T)
		if !ok && m.Items[i] != nil {
			return nil, fmt.Errorf("%w: item %d is not the requested type", errs.ErrInvalidOption, i)
		}
		result = append(result, value)
	}
	return result, nil
}
