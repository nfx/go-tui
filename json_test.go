// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestPrettyJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		contains []string
	}{
		{
			name:  "simple object",
			input: map[string]string{"key": "value"},
			contains: []string{
				"{", "}", ":", "key", "value",
			},
		},
		{
			name:  "simple array",
			input: []string{"item1", "item2"},
			contains: []string{
				"[", "]", "item1", "item2",
			},
		},
		{
			name: "nested object",
			input: map[string]any{
				"outer": map[string]string{
					"inner": "value",
				},
			},
			contains: []string{
				"outer", "inner", "value",
			},
		},
		{
			name:  "string input",
			input: `{"test": "value"}`,
			contains: []string{
				"test", "value",
			},
		},
		{
			name:  "byte slice input",
			input: []byte(`{"test": "value"}`),
			contains: []string{
				"test", "value",
			},
		},
		{
			name: "complex nested structure",
			input: map[string]any{
				"array": []any{
					map[string]string{"nested": "value"},
					"string",
					123,
				},
				"bool":   true,
				"null":   nil,
				"number": 42.5,
			},
			contains: []string{
				"array", "nested", "value", "string", "bool", "true", "null", "number", "42.5",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := PrettyJSON(&buf, tt.input)
			assert.NoError(t, err)

			output := buf.String()
			for _, expected := range tt.contains {
				assert.Contains(t, output, expected)
			}
		})
	}
}

func TestPrettyJSON_InvalidJSON(t *testing.T) {
	var buf bytes.Buffer
	err := PrettyJSON(&buf, make(chan int))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
}

func TestPrettyJSON_InvalidStringInput(t *testing.T) {
	var buf bytes.Buffer
	err := PrettyJSON(&buf, `{"invalid": json}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "indent")
}

func TestPrettyJSON_EscapedCharacters(t *testing.T) {
	var buf bytes.Buffer
	input := map[string]string{
		"escaped": "quote\"backslash\\newline\n",
	}
	err := PrettyJSON(&buf, input)
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "escaped")
	assert.Contains(t, output, "\\\"")
	assert.Contains(t, output, "\\\\")
	assert.Contains(t, output, "\\n")
}

func TestPrettyJSON_ColorCodes(t *testing.T) {
	var buf bytes.Buffer
	input := map[string]any{
		"level1": map[string]any{
			"level2": map[string]string{
				"level3": "value",
			},
		},
	}
	err := PrettyJSON(&buf, input)
	assert.NoError(t, err)

	output := buf.String()
	// Check for ANSI color codes
	assert.Contains(t, output, "\x1b[")
	// Check for bold formatting
	assert.True(t, strings.Contains(output, bold) || strings.Contains(output, "\x1b[1m"))
	// Check for reset codes
	assert.True(t, strings.Contains(output, reset) || strings.Contains(output, "\x1b[0m"))
}
