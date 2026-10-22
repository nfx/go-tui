// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package color

import (
	"fmt"
	"math"
	"strconv"
)

// SRGB holds parsed 8-bit SRGB components.
// see https://en.wikipedia.org/wiki/SRGB.
type SRGB [3]uint8

// Hex returns the canonical uppercase "#RRGGBB" form.
func (c SRGB) Hex() string {
	return fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2])
}

// ParseHex parses "#RRGGBB" or "#RGB", with case-insensitive digits.
func ParseHex(s string) (rgb SRGB, err error) {
	bad := fmt.Errorf("%q: want \"#RRGGBB\" or \"#RGB\"", s)
	if s == "" || s[0] != '#' {
		return rgb, bad
	}
	digits := s[1:]
	switch len(digits) {
	case 3:
		for i := range 3 {
			v, ok := hexDigit(digits[i])
			if !ok {
				return rgb, bad
			}
			rgb[i] = v<<4 | v
		}
	case 6:
		for i := range 3 {
			hi, ok1 := hexDigit(digits[2*i])
			lo, ok2 := hexDigit(digits[2*i+1])
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

// MustHex parses a constant color and panics when it is malformed.
func MustHex(s string) SRGB {
	rgb, err := ParseHex(s)
	if err != nil {
		panic(err)
	}
	return rgb
}

// base is the extended-color SGR parameter: 38 for foreground, 48 for background.
func (SRGB) base(background bool) int {
	if background {
		return 48
	}
	return 38
}

// nearestIndex returns the palette index among the first n entries that is
// closest in [oklab]. Ties go to the lowest index. The distance is always
// measured from the original color, so 16-color output is never derived from
// a 256-color approximation.
func (c SRGB) nearestIndex(n int) int {
	target := c.OKLab()
	lab := ansiPaletteLab()
	best, bestDist := 0, math.Inf(1)
	for i := range n {
		if d := target.distance(lab[i]); d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// neutralChroma is the OKLab chroma below which a color counts as a gray.
// Every hue of the 16 basic colors has at least 0.09.
const neutralChroma = 0.04

// basicIndex is the nearest of the 16 basic colors among the grays for a
// gray and among the hues otherwise. Those few grays sit between many
// mid-tone hues in lightness, so the plain nearest one would turn an accent
// gray and tint a gray red or teal.
func (c SRGB) basicIndex() int {
	i, _ := c.nearestBasic(func(SRGB) bool { return true })
	return i
}

// nearestBasic is [sRGB.basicIndex] among the entries that accept takes, or
// false when it takes none of them.
func (c SRGB) nearestBasic(accept func(SRGB) bool) (int, bool) {
	target := c.OKLab()
	gray := target.lch().C < neutralChroma
	pal, lab := ANSIPalette(), ansiPaletteLab()
	best, bestDist := 0, math.Inf(1)
	for i := range 16 {
		if (i == 0 || i == 7 || i == 8 || i == 15) != gray || !accept(pal[i]) {
			continue
		}
		if d := target.distance(lab[i]); d < bestDist {
			best, bestDist = i, d
		}
	}
	return best, !math.IsInf(bestDist, 1)
}

// ansi16Escape encodes the basic color of c, see [sRGB.basicIndex]. Bright
// colors use the 90/100 series, never bold, so weight and brightness stay
// independent.
func (c SRGB) ansi16Escape(background bool) string {
	i := c.basicIndex()
	var code int
	switch {
	case i < 8 && background:
		code = 40 + i
	case i < 8:
		code = 30 + i
	case background:
		code = 100 + i - 8
	default:
		code = 90 + i - 8
	}
	return "\x1b[" + strconv.Itoa(code) + "m"
}

func hexDigit(b byte) (uint8, bool) {
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
