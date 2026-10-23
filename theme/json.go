// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"fmt"
	"strings"
)

// JSON is the layout of [PrettyJSON].
type JSON struct {
	// Indent is one level of indentation: at most [maxJSONIndent] spaces or
	// tabs, so that the output stays valid JSON. Empty takes two spaces.
	Indent string
	// Compact prints the JSON on one line, without insignificant
	// whitespace, ignoring Indent.
	Compact bool
}

// maxJSONIndent bounds the bytes of one indentation level.
const maxJSONIndent = 16

// normalize takes two spaces for an empty Indent and rejects indentation that is not whitespace.
func (s JSON) normalize() (JSON, error) {
	if s.Indent == "" {
		s.Indent = "  "
	}
	if len(s.Indent) > maxJSONIndent || strings.Trim(s.Indent, " \t") != "" {
		return s, fmt.Errorf("theme.JSON.Indent: %q: want at most %d spaces or tabs", s.Indent, maxJSONIndent)
	}
	return s, nil
}
