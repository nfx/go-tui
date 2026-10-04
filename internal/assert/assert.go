// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

// Package assert provides zero-dependency vendored assertions for testing.
package assert

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Equal compares two values for equality.
func Equal(t *testing.T, expected, actual any) {
	t.Helper()
	expected, actual, res := deepEqual(expected, actual)
	if !res {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d:\nresults differ:\nexpected: %#v\n  actual: %#v", file, line, expected, actual)
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
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d: expected not nil, got nil", file, line)
	}
}

// True asserts that the value is true.
func True(t *testing.T, value bool) { //nolint:revive // ignore
	t.Helper()
	if !value {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d: expected true, got false", file, line)
	}
}

// Contains asserts that the string contains the substring.
func Contains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d: expected %s to contain: %s", file, line, s, substr)
	}
}

// NotContains asserts that the string does not contain the substring.
func NotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d: expected %s not to contain: %s", file, line, s, substr)
	}
}

// Error asserts that the error is not nil.
func Error(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d: expected error, got: nil", file, line)
	}
}

// ErrorIs asserts that the error is of the expected type.
func ErrorIs(t *testing.T, err, expected error) {
	t.Helper()
	if !errors.Is(err, expected) {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d: expected error %v, got: %v", file, line, expected, err)
	}
}

// NoError asserts that the error is nil.
func NoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d: expected no error, got: %v", file, line, err)
	}
}
