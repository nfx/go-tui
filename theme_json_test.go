// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestJSONStyleValidation(t *testing.T) {
	c, err := Theme{}.compile()
	assert.NoError(t, err)
	assert.Equal(t, jsonStyle, c.source.JSON)
	for _, style := range []JSONStyle{
		{Indent: "\t"},
		{Indent: " \t "},
		{Indent: strings.Repeat(" ", maxJSONIndent)},
		{Compact: true},
	} {
		th := NewTheme()
		th.JSON = style
		c, err := th.compile()
		assert.NoError(t, err)
		assert.Equal(t, style.Compact, c.source.JSON.Compact)
		if style.Indent != "" {
			assert.Equal(t, style.Indent, c.source.JSON.Indent)
		} else {
			assert.Equal(t, jsonStyle.Indent, c.source.JSON.Indent)
		}
	}
	for _, style := range []JSONStyle{
		{Indent: "x"},
		{Indent: " \n"},
		{Indent: " "},
		{Indent: "//"},
		{Indent: strings.Repeat(" ", maxJSONIndent+1)},
		{Indent: "-", Compact: true}, // validated even when unused
	} {
		th := NewTheme()
		th.JSON = style
		_, err := th.compile()
		assert.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			fmt.Sprintf("theme.JSON.Indent: %q: want at most 16 spaces or tabs", style.Indent),
		)
	}
}
