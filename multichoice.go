// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"sort"
)

// Multichoice returns checked items in input order. Enter confirms without
// checking the focused item; a successful empty selection is a non-nil slice.
// Items are shallowly copied. Use one active prompt per shared input reader.
func Multichoice[T any](ctx context.Context, label string, items []T, o ...opt) ([]T, error) {
	return pickMultichoice(ctx, label, items, targetOf[T](), o)
}

// pickMultichoice runs a multichoice whose typed defaults have the type of target.
func pickMultichoice[T any](ctx context.Context, label string, items []T, target choiceTarget, o []opt) ([]T, error) {
	if len(items) == 0 {
		return nil, ErrNoItems
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
// Equal formatted keys have unspecified relative order. Templates receive
// entries with Key and Value, including every element of the answer collection.
func MultichoiceKVK[K comparable, V any](ctx context.Context, label string, items map[K]V, o ...opt) ([]K, error) {
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
// Duplicate and non-comparable values remain independently selectable.
func MultichoiceKVV[K comparable, V any](ctx context.Context, label string, items map[K]V, o ...opt) ([]V, error) {
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
	o []opt,
) ([]mapKV[K, V], error) {
	entries := make([]mapKV[K, V], 0, len(items))
	for key, value := range items {
		entries = append(entries, mapKV[K, V]{key, value})
	}
	sort.SliceStable(entries, func(i, j int) bool { return fmt.Sprint(entries[i].Key) < fmt.Sprint(entries[j].Key) })
	// the theme presents the entries, projected to their keys
	return pickMultichoice(ctx, label, entries, target, append([]opt{WithTemplate("(label .Key)")}, o...))
}

// MultichoiceLazy displays a finite sequence progressively and enables Enter
// only after successful exhaustion. Results keep producer arrival order.
// Publication stops on cancellation; iterator work blocked outside yield must
// observe a context supplied by its caller to be interruptible.
func MultichoiceLazy[T any](ctx context.Context, label string, items iter.Seq2[T, error], o ...opt) ([]T, error) {
	if items == nil {
		return nil, fmt.Errorf("%w: MultichoiceLazy sequence is nil", ErrInvalidOption)
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
			return nil, fmt.Errorf("%w: item %d is not the requested type", ErrInvalidOption, i)
		}
		result = append(result, value)
	}
	return result, nil
}

// ErrInvalidOption identifies invalid configuration values.
var ErrInvalidOption = errors.New("invalid option")
