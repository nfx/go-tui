// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestJSONStyleValidation(t *testing.T) {
	n, err := Theme{}.Normalize()
	assert.NoError(t, err)
	assert.Equal(t, JSON{Indent: "  "}, n.JSON)
	for _, style := range []JSON{
		{Indent: "\t"},
		{Indent: " \t "},
		{Indent: strings.Repeat(" ", maxJSONIndent)},
		{Compact: true},
	} {
		th := New()
		th.JSON = style
		n, err := th.Normalize()
		assert.NoError(t, err)
		assert.Equal(t, style.Compact, n.JSON.Compact)
		if style.Indent != "" {
			assert.Equal(t, style.Indent, n.JSON.Indent)
		} else {
			assert.Equal(t, JSON{Indent: "  "}.Indent, n.JSON.Indent)
		}
	}
	for _, style := range []JSON{
		{Indent: "x"},
		{Indent: " \n"},
		{Indent: " "},
		{Indent: "//"},
		{Indent: strings.Repeat(" ", maxJSONIndent+1)},
		{Indent: "-", Compact: true}, // validated even when unused
	} {
		th := New()
		th.JSON = style
		_, err := th.Normalize()
		assert.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			fmt.Sprintf("theme.JSON.Indent: %q: want at most 16 spaces or tabs", style.Indent),
		)
	}
}
