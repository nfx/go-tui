// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"github.com/nfx/go-tui/theme"
	"testing"
)

func TestJSONIndentFormats(t *testing.T) {
	type data struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	buf, err := jsonIndent(data{Name: "Alice", Age: 30}, theme.New().JSON)
	if err != nil {
		t.Fatalf("jsonIndent failed: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"name": "Alice"`)) {
		t.Fatalf("expected name field")
	}
}

func TestJSONIndentInvalidJSON(t *testing.T) {
	_, err := jsonIndent("{", theme.New().JSON)
	if err == nil {
		t.Fatalf("expected error")
	}
}
