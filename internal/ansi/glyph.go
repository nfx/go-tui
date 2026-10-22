// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package ansi

import (
	"slices"
	"unicode"
)

// glyph is a code point, as a terminal shows it.
type glyph rune

// glyphFlags are the Unicode properties of a rune that [glyphRanges] records.
type glyphFlags uint8

const (
	wideGlyph  glyphFlags = 1 << iota // East_Asian_Width W or F
	pictGlyph                         // Extended_Pictographic
	emojiGlyph                        // Emoji
)

//go:generate sh -c "go run glyphs_gen.go | gofmt > glyphs.go"

type glyphRange struct {
	lo, hi rune
	flags  glyphFlags
}

// has reports whether r has all the properties p.
func (r glyph) has(p glyphFlags) bool {
	i, found := slices.BinarySearchFunc(glyphRanges, rune(r), func(c glyphRange, r rune) int {
		switch {
		case r < c.lo:
			return 1
		case r > c.hi:
			return -1
		default:
			return 0
		}
	})
	return found && glyphRanges[i].flags&p == p
}

// isRegionalIndicator reports whether r participates in a flag pair.
func (r glyph) isRegionalIndicator() bool {
	return r >= 0x1f1e6 && r <= 0x1f1ff
}

// isZeroWidth reports whether r occupies no column by itself.
func (r glyph) isZeroWidth() bool {
	if r < 0x300 {
		return r == 0xad // soft hyphen
	}
	return unicode.In(rune(r), unicode.Mn, unicode.Me, unicode.Mc, unicode.Cf) ||
		(r >= 0x1160 && r <= 0x11ff) || (r >= 0xd7b0 && r <= 0xd7ff)
}

// width returns r's standalone terminal width.
func (r glyph) width() int {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	// see unicode combining marks in https://www.unicode.org/reports/tr29/
	case r.isZeroWidth():
		return 0
	case r >= 0x1100 && r.has(wideGlyph):
		return 2
	default:
		return 1
	}
}
