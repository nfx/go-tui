// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
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
func (c RGB) must() sRGB {
	rgb, err := c.parse()
	if err != nil {
		panic(err)
	}
	return rgb
}

// parse returns the color bytes. The empty (unset) value parses to zero
// bytes without error, so callers that care check c == "" themselves.
func (c RGB) parse() (rgb sRGB, err error) {
	s := string(c)
	if s == "" {
		return rgb, nil
	}
	bad := fmt.Errorf("%q: want \"#RRGGBB\" or \"#RGB\"", s)
	if s[0] != '#' {
		return rgb, bad
	}
	digits := s[1:]
	switch len(digits) {
	case 3:
		for i := range 3 {
			v, ok := c.hexDigit(digits[i])
			if !ok {
				return rgb, bad
			}
			rgb[i] = v<<4 | v
		}
	case 6:
		for i := range 3 {
			hi, ok1 := c.hexDigit(digits[2*i])
			lo, ok2 := c.hexDigit(digits[2*i+1])
			if !ok1 || !ok2 {
				return rgb, bad
			}
			rgb[i] = hi<<4 | lo
		}
	default:
		return rgb, bad
	}
	return rgb, nil
}

func (c RGB) hexDigit(b byte) (uint8, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}
