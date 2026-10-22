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
	buf, err := jsonIndent(data{Name: "Alice", Age: 30}, jsonStyle)
	if err != nil {
		t.Fatalf("jsonIndent failed: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"name": "Alice"`)) {
		t.Fatalf("expected name field")
	}
}

func TestJSONIndentInvalidJSON(t *testing.T) {
	_, err := jsonIndent("{", jsonStyle)
	if err == nil {
		t.Fatalf("expected error")
	}
}
