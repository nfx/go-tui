// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"
)

func TestAnsciiFormatterHandlesTypes(t *testing.T) {
	str := "hi"
	ptr := &str
	arr := []string{"alpha", "beta"}
	num := 5
	formatter := ansciiFormatter("\x1b[31m", "\x1b[1m")

	got := formatter(ptr, arr, &num, "end")
	if !strings.HasPrefix(got, "\x1b[31m\x1b[1m") {
		t.Fatalf("expected prefix codes, got %q", got)
	}
	if !strings.Contains(got, "alpha, beta") {
		t.Fatalf("expected joined array, got %q", got)
	}
	if !strings.Contains(got, "5") {
		t.Fatalf("expected pointer value, got %q", got)
	}
	if !strings.HasSuffix(got, reset) {
		t.Fatalf("expected reset suffix, got %q", got)
	}
}

func TestWithFnRegistersFunc(t *testing.T) {
	name := "testFunc"
	orig, had := colorFns[name]
	WithFn(name, func(...any) string { return "ok" })
	t.Cleanup(func() {
		if had {
			colorFns[name] = orig
		} else {
			delete(colorFns, name)
		}
	})

	raw, ok := colorFns[name]
	if !ok {
		t.Fatalf("registration failed")
	}
	fn, ok := raw.(func(...any) string)
	if !ok {
		t.Fatalf("unexpected function type")
	}
	if fn() != "ok" {
		t.Fatalf("unexpected return value")
	}
}
