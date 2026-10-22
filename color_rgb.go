// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"math"
	"strconv"
)

// sRGB holds parsed 8-bit sRGB components.
// see https://en.wikipedia.org/wiki/SRGB.
type sRGB [3]uint8

// hex returns the canonical uppercase "#RRGGBB" form.
func (c sRGB) hex() RGB {
	return RGB(fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2]))
}

// base is the extended-color SGR parameter: 38 for foreground, 48 for background.
func (sRGB) base(background bool) int {
	if background {
		return 48
	}
	return 38
}

// nearestIndex returns the palette index among the first n entries that is
// closest in [oklab]. Ties go to the lowest index. The distance is always
// measured from the original color, so 16-color output is never derived from
// a 256-color approximation.
func (c sRGB) nearestIndex(n int) int {
	target := c.oklab()
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
func (c sRGB) basicIndex() int {
	i, _ := c.nearestBasic(func(sRGB) bool { return true })
	return i
}

// nearestBasic is [sRGB.basicIndex] among the entries that accept takes, or
// false when it takes none of them.
func (c sRGB) nearestBasic(accept func(sRGB) bool) (int, bool) {
	target := c.oklab()
	gray := target.lch().C < neutralChroma
	pal, lab := ansiPalette(), ansiPaletteLab()
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
func (c sRGB) ansi16Escape(background bool) string {
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
