// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// jsonIndent validates src and lays it out in style, without a trailing newline.
func jsonIndent(src any, style JSONStyle) (*bytes.Buffer, error) {
	identBuf := bytes.NewBuffer(nil)
	var toIndent []byte
	var err error
	switch src := src.(type) {
	case string:
		toIndent = []byte(src)
	case []byte:
		toIndent = src
	default:
		toIndent, err = json.Marshal(src)
		if err != nil {
			return nil, fmt.Errorf("marshal: %w", err)
		}
	}
	// standard library already indents JSON data, so we're only coloring things here.
	if style.Compact {
		err = json.Compact(identBuf, toIndent)
	} else {
		err = json.Indent(identBuf, toIndent, "", style.Indent)
	}
	if err != nil {
		return nil, fmt.Errorf("indent: %w", err)
	}
	return identBuf, nil
}
