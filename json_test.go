// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"testing"
)

func TestJSONIndentFormats(t *testing.T) {
	type data struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	buf, err := jsonIndent(data{Name: "Alice", Age: 30})
	if err != nil {
		t.Fatalf("jsonIndent failed: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"name": "Alice"`)) {
		t.Fatalf("expected name field")
	}
}

func TestJSONIndentInvalidJSON(t *testing.T) {
	_, err := jsonIndent("{")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestPrettyJSONColor(t *testing.T) {
	origChecker := terminalChecker
	t.Cleanup(func() {
		terminalChecker = origChecker
	})
	terminalChecker = func() bool { return true }

	var out bytes.Buffer
	err := PrettyJSON(&out, `{"hello":["world", "again"], "path":"a\\b"}`)
	if err != nil {
		t.Fatalf("PrettyJSON failed: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("\x1b[")) {
		t.Fatalf("expected color escape codes")
	}
}

func TestPrettyJSONNoColor(t *testing.T) {
	origChecker := terminalChecker
	t.Cleanup(func() {
		terminalChecker = origChecker
	})
	terminalChecker = func() bool { return false }

	var out bytes.Buffer
	err := PrettyJSON(&out, `{"flat":true}`)
	if err != nil {
		t.Fatalf("PrettyJSON failed: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"flat": true`)) {
		t.Fatalf("expected formatted JSON")
	}
}

func TestPrettyJsonBackslash(t *testing.T) {
	stack := []jsonState{jsonQuoted}
	var out bytes.Buffer
	stack = pretttJsonBackslash(stack, &out, '\\')
	if len(stack) != 2 {
		t.Fatalf("expected escape state")
	}
	if out.String() != "\\" {
		t.Fatalf("expected backslash output")
	}
}

func TestAbs(t *testing.T) {
	if abs(-2) != 2 {
		t.Fatalf("expected abs")
	}
	if abs(3) != 3 {
		t.Fatalf("expected abs")
	}
}

func TestPrettyJsonLoopEscape(t *testing.T) {
	stack := []jsonState{jsonQuoted, jsonEscape}
	var out bytes.Buffer
	_, stack = prettyJsonLoop(stack, &out, 'x', 0)
	if out.String() != "x" {
		t.Fatalf("expected escaped output")
	}
	if len(stack) != 1 || stack[0] != jsonQuoted {
		t.Fatalf("unexpected stack")
	}
}
