// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"
)

// JSONStyle is the layout of [PrettyJSON].
type JSONStyle struct {
	// Indent is one level of indentation: at most [maxJSONIndent] spaces or
	// tabs, so that the output stays valid JSON. Empty takes two spaces.
	Indent string
	// Compact prints the JSON on one line, without insignificant
	// whitespace, ignoring Indent.
	Compact bool
}

// jsonStyle is the theme preset: the legacy two-space indentation.
var jsonStyle = JSONStyle{Indent: "  "}

// maxJSONIndent bounds the bytes of one indentation level.
const maxJSONIndent = 16

// normalize fills the preset and rejects indentation that is not whitespace.
func (s JSONStyle) normalize() (JSONStyle, error) {
	if s.Indent == "" {
		s.Indent = jsonStyle.Indent
	}
	if len(s.Indent) > maxJSONIndent || strings.Trim(s.Indent, " \t") != "" {
		return s, fmt.Errorf("theme.JSON.Indent: %q: want at most %d spaces or tabs", s.Indent, maxJSONIndent)
	}
	return s, nil
}
