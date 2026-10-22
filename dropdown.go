// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"sort"
)

type mapKV[K comparable, V any] struct {
	Key   K
	Value V
}

// mapKey projects the key of a map entry that the map widgets built.
func mapKey[K comparable, V any](item any) any {
	kv, _ := item.(mapKV[K, V]) //nolint:errcheck // the widget only holds its own entries
	return kv.Key
}

// mapValue projects the value of a map entry that the map widgets built.
func mapValue[K comparable, V any](item any) any {
	kv, _ := item.(mapKV[K, V]) //nolint:errcheck // the widget only holds its own entries
	return kv.Value
}

func DropdownKV[K comparable, V any](ctx context.Context, label string, items map[K]V, opts ...opt) (K, V, error) {
	var zeroK K
	var zeroV V
	if len(items) == 0 {
		return zeroK, zeroV, ErrNoItems
	}
	kvs := make([]mapKV[K, V], 0, len(items))
	for k, v := range items {
		kvs = append(kvs, mapKV[K, V]{k, v})
	}
	sort.Slice(kvs, func(i, j int) bool {
		// sort by key, use fmt to convert to string
		return fmt.Sprintf("%v", kvs[i].Key) < fmt.Sprintf("%v", kvs[j].Key)
	})
	// the theme presents the entries, projected to their keys
	opts = append([]opt{WithTemplate("(label .Key)")}, opts...)
	anyKVs := make([]any, len(kvs))
	for i, kv := range kvs {
		anyKVs[i] = kv
	}
	target := choiceTarget{
		typ:  reflect.TypeFor[K](),
		proj: mapKey[K, V],
	}
	i, err := pickIndex(ctx, label, anyKVs, target, opts)
	if err != nil {
		return zeroK, zeroV, err
	}
	return kvs[i].Key, kvs[i].Value, nil
}

func Dropdown[T any](ctx context.Context, label string, items []T, opts ...opt) (T, error) {
	var zero T
	if len(items) == 0 {
		return zero, ErrNoItems
	}
	// apparently, there's no other non-reflective way around
	anyItems := make([]any, len(items))
	for i, v := range items {
		anyItems[i] = v
	}
	i, err := pickIndex(ctx, label, anyItems, targetOf[T](), opts)
	if err != nil {
		return zero, err
	}
	// we know i is valid
	return items[i], nil
}

func DropdownLazy[V any](ctx context.Context, label string, itemFn iter.Seq2[V, error], o ...opt) (V, error) {
	var zero V
	d := newDropdown(ctx)
	d.Label = label
	d.target = targetOf[V]()
	err := opts(o).Apply(d)
	if err != nil {
		return zero, err
	}
	// validate everything that can fail before the producer starts
	err = d.parseTemplates()
	if err != nil {
		return zero, fmt.Errorf("templates: %w", err)
	}
	seq := iter.Seq2[any, error](func(yield func(any, error) bool) {
		for v, err := range itemFn {
			if !yield(v, err) {
				return
			}
		}
	})
	terminal := outputIsTerminal(d.out)
	// the producer is owned by this call: it stops on every return path
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.Ctx = ctx
	var i int
	if terminal {
		d.startLazyProducer(ctx, seq)
		i, err = d.selectIndex(ctx)
	} else {
		i, err = d.answerLazyFromDefault(ctx, seq)
	}
	if err != nil {
		return zero, err
	}
	item := d.Items[i]
	if terminal {
		err = d.themedAnswer(label, i)
		if err != nil {
			return zero, fmt.Errorf("answer: %w", err)
		}
	}
	valid, ok := item.(V)
	if !ok && item != nil { // should never happen
		return zero, fmt.Errorf("%w: expected %T, got %T", ErrInvalidState, valid, item)
	}
	return valid, nil
}

func DropdownIndex(ctx context.Context, label string, items []any, o ...opt) (int, error) {
	return pickIndex(ctx, label, items, choiceTarget{}, o)
}

// pickIndex runs a dropdown whose typed defaults have the type of target.
func pickIndex(ctx context.Context, label string, items []any, target choiceTarget, o []opt) (int, error) {
	d := newDropdown(ctx)
	d.target = target
	err := opts(o).Apply(d)
	if err != nil {
		return -1, err
	}
	return d.pick(ctx, label, items)
}

// WithHide skips printing the answer of a dropdown or an input.
func WithHide() opt {
	return opT(func(x withPrompt) error {
		x.setHide()
		return nil
	})
}
