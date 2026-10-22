// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"math"
)

// srgbToLinear undoes the sRGB transfer function for one 8-bit component.
func (c sRGB) srgbToLinear(v uint8) float64 {
	f := float64(v) / 255
	if f <= 0.04045 {
		return f / 12.92
	}
	return math.Pow((f+0.055)/1.055, 2.4)
}

// contrast is the WCAG contrast ratio between two colors, in 1..21.
func (c sRGB) contrast(o sRGB) float64 {
	a, b := c.luminance(), o.luminance()
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// luminance is the linear-sRGB relative luminance.
func (c sRGB) luminance() float64 {
	return 0.2126*c.srgbToLinear(c[0]) + 0.7152*c.srgbToLinear(c[1]) + 0.0722*c.srgbToLinear(c[2])
}

// isLight classifies a background as light, at or above [lightBoundary].
func (c sRGB) isLight() bool { return c.luminance() >= lightBoundary }

// atLeast returns c unchanged when it has at least the target contrast
// against bg. Otherwise it returns the same hue at the smallest lightness
// change that does, in whichever direction needs less. When neither black nor
// white suffices, the one with more contrast is returned.
func (c sRGB) atLeast(bg sRGB, target float64) sRGB {
	if c.contrast(bg) >= target {
		return c
	}
	lch := c.oklab().lch()
	var best sRGB
	bestStep, found := math.Inf(1), false
	for _, far := range []float64{0, 1} {
		l, ok := lch.reach(bg, target, far)
		if !ok {
			continue
		}
		if step := math.Abs(l - lch.L); step < bestStep {
			best, bestStep, found = lch.withLightness(l), step, true
		}
	}
	if found {
		return best
	}
	black, white := lch.withLightness(0), lch.withLightness(1)
	if black.contrast(bg) >= white.contrast(bg) {
		return black
	}
	return white
}

// atContrast is the color of c's hue closest to bg that reaches the target
// contrast, moving away from bg toward black on light backgrounds and toward
// white on dark ones. It may be less contrasty than c itself. When even the
// extreme falls short, that extreme is returned.
func (c sRGB) atContrast(bg sRGB, target float64) sRGB {
	far := 1.0
	if bg.isLight() {
		far = 0.0
	}
	lch := c.oklab().lch()
	l, _ := lch.reach(bg, target, far)
	return lch.withLightness(l)
}

// reach finds the lightness nearest bg's, on the side of far, whose color
// reaches the target contrast. When even far falls short, it returns far and
// false.
func (c oklch) reach(bg sRGB, target, far float64) (float64, bool) {
	if c.withLightness(far).contrast(bg) < target {
		return far, false
	}
	// contrast grows as lightness moves away from the background
	return bisect(far, bg.oklab().L, func(l float64) bool {
		return c.withLightness(l).contrast(bg) >= target
	}), true
}

// quieter returns the color between c and bg closest to bg that still has
// the target contrast. When c already falls short, c is returned.
func (c sRGB) quieter(bg sRGB, target float64) sRGB {
	if c.contrast(bg) <= target {
		return c
	}
	a, b := c.oklab(), bg.oklab()
	// 0 is c, 1 is bg
	return a.mix(b, bisect(0, 1, func(t float64) bool {
		return a.mix(b, t).contrast(bg) >= target
	}))
}

// bisect narrows the interval between good, where ok holds, and bad, where
// it does not, and returns the boundary point on the good side.
func bisect(good, bad float64, ok func(float64) bool) float64 {
	for range 32 {
		mid := (good + bad) / 2
		if ok(mid) {
			good = mid
		} else {
			bad = mid
		}
	}
	return good
}

// ramp returns n shades of base's hue with ascending contrast against bg,
// from [contrastMark] to [contrastRampMax]. One level is the base itself.
func (c sRGB) ramp(bg sRGB, n int) ([]sRGB, error) {
	if n < 1 || n > maxRampLevels {
		return nil, fmt.Errorf("levels %d: want 1..%d", n, maxRampLevels)
	}
	if n == 1 {
		return []sRGB{c}, nil
	}
	out := make([]sRGB, n)
	for i := range n {
		target := contrastMark + (contrastRampMax-contrastMark)*float64(i)/float64(n-1)
		out[i] = c.atContrast(bg, target)
	}
	return out, nil
}

// Contrast targets, see https://www.w3.org/WAI/WCAG21/Techniques/general/G18.
const (
	// contrastText is for roles that carry words: labels, keys, reasons.
	contrastText = 4.5
	// contrastMark is for glyphs and markers.
	contrastMark = 3.0
	// contrastGuide is for nonessential structure, which may be quiet.
	contrastGuide = 2.0
	// contrastRampMax is the strongest level of a shade ramp.
	contrastRampMax = 7.0

	// maxRampLevels bounds a shade ramp.
	maxRampLevels = 16

	// lightBoundary is the relative luminance where ideal black and white
	// have equal contrast: sqrt(0.05*1.05) - 0.05. A design choice rather
	// than a terminal protocol rule.
	lightBoundary = 0.1791287847
)
