// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
)

// named returns the glyph of a field by name.
func (s Symbols) named(name string) (string, error) {
	switch name {
	case "Prompt":
		return s.Prompt, nil
	case "Focus":
		return s.Focus, nil
	case "Guide":
		return s.Guide, nil
	case "End":
		return s.End, nil
	case "Checked":
		return s.Checked, nil
	case "Unchecked":
		return s.Unchecked, nil
	case "Success":
		return s.Success, nil
	case "Error":
		return s.Error, nil
	case "Cancel":
		return s.Cancel, nil
	case "Ellipsis":
		return s.Ellipsis, nil
	case "Mask":
		return s.Mask, nil
	case "Up":
		return s.Up, nil
	case "Down":
		return s.Down, nil
	case "BarFill":
		return s.BarFill, nil
	case "BarHead":
		return s.BarHead, nil
	case "BarEmpty":
		return s.BarEmpty, nil
	case "Unknown":
		return s.Unknown, nil
	case "Separator":
		return s.Separator, nil
	}
	return "", fmt.Errorf("symbol %q: no such symbol", name)
}

// arrows names the up and down arrow keys in key hints.
func (s Symbols) arrows() ansi.Text { return ansi.Text(s.Up + s.Down) }
