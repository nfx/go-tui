// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

// Package assert provides zero-dependency vendored assertions for testing.
package assert

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Equal compares two values for equality.
func Equal(t *testing.T, expected, actual any) {
	t.Helper()
	expected, actual, res := deepEqual(expected, actual)
	if !res {
		t.Fatalf("results differ:\nexpected: %#v\n  actual: %#v", expected, actual)
	}
}

func deepEqual(expected, actual any) (want, got any, ok bool) {
	ev, av := reflect.ValueOf(expected), reflect.ValueOf(actual)
	if !ev.IsValid() || !av.IsValid() {
		return expected, actual, expected == nil && actual == nil
	}
	if ev.Type().ConvertibleTo(av.Type()) {
		expected = ev.Convert(av.Type()).Interface()
	} else if av.Type().ConvertibleTo(ev.Type()) {
		actual = av.Convert(ev.Type()).Interface()
	}
	res := reflect.DeepEqual(expected, actual)
	return expected, actual, res
}

// NotNil asserts that the value is not nil.
func NotNil(t *testing.T, value any) {
	t.Helper()
	var fail bool
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Invalid:
		fail = true
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		fail = v.IsNil()
	}
	if fail {
		t.Fatal("expected not nil, got nil")
	}
}

// True asserts that the value is true.
func True(t *testing.T, value bool) { //nolint:revive // ignore
	t.Helper()
	if !value {
		t.Fatal("expected true, got false")
	}
}

// Contains asserts that the string contains the substring.
func Contains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("expected %s to contain: %s", s, substr)
	}
}

// NotContains asserts that the string does not contain the substring.
func NotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Fatalf("expected %s not to contain: %s", s, substr)
	}
}

// Error asserts that the error is not nil.
func Error(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got: nil")
	}
}

// ErrorIs asserts that the error is of the expected type.
func ErrorIs(t *testing.T, err, expected error) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("expected error %v, got: %v", expected, err)
	}
}

// NoError asserts that the error is nil.
func NoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
