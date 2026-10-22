// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package color

import (
	"fmt"
	"math"
)

// srgbToLinear undoes the sRGB transfer function for one 8-bit component.
func (c SRGB) srgbToLinear(v uint8) float64 {
	f := float64(v) / 255
	if f <= 0.04045 {
		return f / 12.92
	}
	return math.Pow((f+0.055)/1.055, 2.4)
}

// Contrast is the WCAG Contrast ratio between two colors, in 1..21.
func (c SRGB) Contrast(o SRGB) float64 {
	a, b := c.luminance(), o.luminance()
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// luminance is the linear-sRGB relative luminance.
func (c SRGB) luminance() float64 {
	return 0.2126*c.srgbToLinear(c[0]) + 0.7152*c.srgbToLinear(c[1]) + 0.0722*c.srgbToLinear(c[2])
}

// IsLight classifies a background as light, at or above [lightBoundary].
func (c SRGB) IsLight() bool { return c.luminance() >= lightBoundary }

// AtLeast returns c unchanged when it has at least the target contrast
// against bg. Otherwise it returns the same hue at the smallest lightness
// change that does, in whichever direction needs less. When neither black nor
// white suffices, the one with more contrast is returned.
func (c SRGB) AtLeast(bg SRGB, target float64) SRGB {
	if c.Contrast(bg) >= target {
		return c
	}
	lch := c.OKLab().lch()
	var best SRGB
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
	if black.Contrast(bg) >= white.Contrast(bg) {
		return black
	}
	return white
}

// atContrast is the color of c's hue closest to bg that reaches the target
// contrast, moving away from bg toward black on light backgrounds and toward
// white on dark ones. It may be less contrasty than c itself. When even the
// extreme falls short, that extreme is returned.
func (c SRGB) atContrast(bg SRGB, target float64) SRGB {
	far := 1.0
	if bg.IsLight() {
		far = 0.0
	}
	lch := c.OKLab().lch()
	l, _ := lch.reach(bg, target, far)
	return lch.withLightness(l)
}

// reach finds the lightness nearest bg's, on the side of far, whose color
// reaches the target contrast. When even far falls short, it returns far and
// false.
func (c oklch) reach(bg SRGB, target, far float64) (float64, bool) {
	if c.withLightness(far).Contrast(bg) < target {
		return far, false
	}
	// contrast grows as lightness moves away from the background
	return bisect(far, bg.OKLab().L, func(l float64) bool {
		return c.withLightness(l).Contrast(bg) >= target
	}), true
}

// Quieter returns the color between c and bg closest to bg that still has
// the target contrast. When c already falls short, c is returned.
func (c SRGB) Quieter(bg SRGB, target float64) SRGB {
	if c.Contrast(bg) <= target {
		return c
	}
	a, b := c.OKLab(), bg.OKLab()
	// 0 is c, 1 is bg
	return a.Mix(b, bisect(0, 1, func(t float64) bool {
		return a.Mix(b, t).Contrast(bg) >= target
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
func (c SRGB) ramp(bg SRGB, n int) ([]SRGB, error) {
	if n < 1 || n > maxRampLevels {
		return nil, fmt.Errorf("levels %d: want 1..%d", n, maxRampLevels)
	}
	if n == 1 {
		return []SRGB{c}, nil
	}
	out := make([]SRGB, n)
	for i := range n {
		target := ContrastMark + (contrastRampMax-ContrastMark)*float64(i)/float64(n-1)
		out[i] = c.atContrast(bg, target)
	}
	return out, nil
}

// Contrast targets, see https://www.w3.org/WAI/WCAG21/Techniques/general/G18.
const (
	// ContrastText is for roles that carry words: labels, keys, reasons.
	ContrastText = 4.5
	// ContrastMark is for glyphs and markers.
	ContrastMark = 3.0
	// ContrastGuide is for nonessential structure, which may be quiet.
	ContrastGuide = 2.0
	// contrastRampMax is the strongest level of a shade ramp.
	contrastRampMax = 7.0

	// maxRampLevels bounds a shade ramp.
	maxRampLevels = 16

	// lightBoundary is the relative luminance where ideal black and white
	// have equal contrast: sqrt(0.05*1.05) - 0.05. A design choice rather
	// than a terminal protocol rule.
	lightBoundary = 0.1791287847
)
