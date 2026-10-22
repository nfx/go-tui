// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/render"
	"reflect"
	"strconv"
)

// themedHint renders slot for keys, led by the count of hidden items.
func themedHint(slot render.BoundSlot, keys []render.Hint, hidden int) (ansi.Text, error) {
	if hidden <= 0 {
		return slot.Render(render.HintView(keys))
	}
	view := append(render.HintView{{Left: ansi.Text(strconv.Itoa(hidden)), Right: ansi.Text("more")}}, keys...)
	return slot.Render(view)
}

var anyType = reflect.TypeFor[any]()

// choiceTarget describes the type a typed default of a choice widget has: the
// type of the returned item, key or value, and how to project it from a source
// entry. A zero target accepts any value and matches whole entries.
type choiceTarget struct {
	typ  reflect.Type
	proj func(item any) any
}

// targetOf is the target of a widget returning T.
func targetOf[T any]() choiceTarget { return choiceTarget{typ: reflect.TypeFor[T]()} }

func (c choiceTarget) elem() reflect.Type {
	if c.typ == nil {
		return anyType
	}
	return c.typ
}

func (c choiceTarget) project(item any) any {
	if c.proj == nil {
		return item
	}
	return c.proj(item)
}

// nilable reports whether values of typ can be nil.
func (c choiceTarget) nilable(typ reflect.Type) bool {
	switch typ.Kind() { //nolint:exhaustive // only these kinds can be nil
	case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan,
		reflect.UnsafePointer:
		return true
	}
	return false
}

func (c choiceTarget) isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return c.nilable(rv.Type()) && rv.IsNil()
}

// accepts reports whether d is assignable to the target type. It does not coerce.
func (c choiceTarget) accepts(d any) bool {
	if d == nil {
		return c.nilable(c.elem())
	}
	return reflect.TypeOf(d).AssignableTo(c.elem())
}

// matches returns the indexes of entries whose projection equals d. The caller
// has checked [choiceTarget.accepts].
func (c choiceTarget) matches(d any, items []any) ([]int, error) {
	want := d
	if d != nil {
		rv := reflect.ValueOf(d)
		if rv.Kind() == reflect.Func && !rv.IsNil() {
			return nil, errBadDefault("%T is a function, which cannot be identified", d)
		}
		if typ := c.elem(); typ.Kind() != reflect.Interface {
			// assignable types may differ, e.g. a named slice and an unnamed one
			nv := reflect.New(typ).Elem()
			nv.Set(rv)
			want = nv.Interface()
		}
	}
	var out []int
	for i, item := range items {
		got := c.project(item)
		if d == nil {
			if c.isNil(got) {
				out = append(out, i)
			}
			continue
		}
		if c.equalValues(want, got) {
			out = append(out, i)
		}
	}
	return out, nil
}

// equalValues compares with Go equality when that is safe for the dynamic
// values, and structurally otherwise. Pointers compare by identity.
func (c choiceTarget) equalValues(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Type() != vb.Type() {
		return false
	}
	if va.Comparable() && vb.Comparable() {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}

// unique returns the only match, or the reason there is none.
func uniqueMatch(matches []int, what string) (int, error) {
	switch len(matches) {
	case 0:
		return -1, errBadDefault("%s matches no entry", what)
	case 1:
		return matches[0], nil
	}
	return -1, errBadDefault("%s matches %d entries", what, len(matches))
}

// resolveSingle finds the one entry a default identifies. A value of the target
// type is matched exactly first; a string without an exact match is a prefix hint.
func (c choiceTarget) resolveSingle(d any, items []any, hint func(string) ([]int, error)) (int, error) {
	if c.accepts(d) {
		matches, err := c.matches(d, items)
		if err != nil {
			return -1, err
		}
		if len(matches) > 0 {
			return uniqueMatch(matches, fmt.Sprintf("%T value", d))
		}
	}
	s, ok := d.(string)
	if !ok {
		return -1, errBadDefault("%T matches no entry", d)
	}
	matches, err := hint(s)
	if err != nil {
		return -1, err
	}
	return uniqueMatch(matches, fmt.Sprintf("hint %q", s))
}
