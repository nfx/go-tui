// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"github.com/nfx/go-tui/internal/color"
)

// RGB is a 24-bit sRGB color in CSS hex notation, "#RRGGBB" or the
// shorthand "#RGB". Hex digits are case-insensitive. The empty string
// means unset: the slot inherits or derives its color.
//
// RGB values must not be compared with ==: "#0AD", "#00aadd" and "#00AADD"
// are the same color.
type RGB string

// Validate returns nil when c is empty or well-formed hex.
func (c RGB) Validate() error {
	_, err := c.parse()
	return err
}

// must parses a color that was already validated or is a constant.
func (c RGB) must() color.SRGB {
	rgb, err := c.parse()
	if err != nil {
		panic(err)
	}
	return rgb
}

// parse returns the color bytes. The empty (unset) value parses to zero
// bytes without error, so callers that care check c == "" themselves.
func (c RGB) parse() (color.SRGB, error) {
	if c == "" {
		return color.SRGB{}, nil
	}
	return color.ParseHex(string(c))
}
