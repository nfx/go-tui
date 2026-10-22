// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package color

import (
	"math"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestOKLabReferenceValues(t *testing.T) {
	assert.Equal(t, oklab{}, SRGB{0, 0, 0}.OKLab())
	white := SRGB{255, 255, 255}.OKLab()
	assert.True(t, white.distance(oklab{L: 1}) <= 1e-6)
	// Ottosson's published reference: pure red is about (0.628, 0.225, 0.126).
	red := SRGB{255, 0, 0}.OKLab()
	assert.True(t, red.distance(oklab{L: 0.6280, A: 0.2249, B: 0.1258}) <= 1e-5)
}

func TestContrastReferenceValues(t *testing.T) {
	black, white := SRGB{0, 0, 0}, SRGB{255, 255, 255}
	assert.True(t, black.Contrast(white) > 20.99 && black.Contrast(white) <= 21)
	assert.Equal(t, 1.0, white.Contrast(white))
	assert.Equal(t, white.Contrast(black), black.Contrast(white))
	assert.True(t, black.Contrast(SRGB{0x77, 0x77, 0x77}) > 4.6)
}

func TestLightBoundaryEqualisesBlackAndWhite(t *testing.T) {
	// A background exactly at the boundary has equal contrast to both.
	y := lightBoundary
	assert.True(t, math.Abs((1.05)/(y+0.05)-(y+0.05)/0.05) < 1e-6)
	assert.True(t, SRGB{255, 255, 255}.IsLight())
	assert.True(t, !SRGB{0, 0, 0}.IsLight())
	assert.True(t, !SRGB{0x12, 0x12, 0x12}.IsLight())
	assert.True(t, SRGB{0x80, 0x80, 0x80}.IsLight())
	assert.True(t, !SRGB{0, 0, 0x80}.IsLight()) // dark blue
}

func TestOKLabRoundTrip(t *testing.T) {
	for r := 0; r < 256; r += 51 {
		for g := 0; g < 256; g += 51 {
			for b := 0; b < 256; b += 51 {
				c := SRGB{uint8(r), uint8(g), uint8(b)}
				back, ok := c.OKLab().srgb()
				assert.True(t, ok)
				assert.Equal(t, c, back)
			}
		}
	}
}

func TestGamutMappingKeepsHueAndFits(t *testing.T) {
	// Far outside sRGB at this lightness: chroma must shrink, not clip channels.
	lch := oklch{L: 0.9, C: 0.4, H: 2.0}
	got := lch.srgb()
	back := got.OKLab().lch()
	assert.True(t, back.C < lch.C)
	d := back.H - lch.H
	assert.True(t, d < 0.1 && d > -0.1)
}

func TestSeedKeptWhenReadable(t *testing.T) {
	accent := mustHex("#00ADD8")
	// Bright cyan is readable on a dark background: rendered literally.
	assert.Equal(t, accent, accent.AtLeast(SRGB{0x12, 0x12, 0x12}, ContrastText))
	// But not on white.
	got := accent.AtLeast(SRGB{255, 255, 255}, ContrastText)
	assert.True(t, got != accent)
	assert.True(t, got.Contrast(SRGB{255, 255, 255}) >= ContrastText)
	assert.True(t, got.luminance() < accent.luminance())
}

func TestAtLeastIsSmallestStep(t *testing.T) {
	white := SRGB{255, 255, 255}
	got := mustHex("#00ADD8").AtLeast(white, ContrastText)
	assert.True(t, got.Contrast(white) < ContrastText+0.15)
}

func TestAtLeastPicksNearerDirection(t *testing.T) {
	// on a mid-gray background, a dark seed is closer to passing by darkening
	seed, bg := mustHex("#303030"), mustHex("#606060")
	got := seed.AtLeast(bg, ContrastMark)
	assert.True(t, got.Contrast(bg) >= ContrastMark)
	assert.True(t, got.luminance() < bg.luminance())
	assert.True(t, math.Abs(got.OKLab().L-seed.OKLab().L) < 0.2)
	// and a light seed is closer by lightening
	light := mustHex("#C0C0C0").AtLeast(bg, ContrastMark)
	assert.True(t, light.luminance() > bg.luminance())
}

func TestAtLeastKeepsHue(t *testing.T) {
	seed := mustHex("#C83741")
	got := seed.AtLeast(SRGB{0, 0, 0}, 9)
	d := got.OKLab().lch().H - seed.OKLab().lch().H
	assert.True(t, d < 0.15 && d > -0.15)
	assert.True(t, got.Contrast(SRGB{0, 0, 0}) >= 9)
}

func TestAtLeastUnreachableReturnsExtreme(t *testing.T) {
	mid := SRGB{0x77, 0x77, 0x77}
	assert.Equal(t, SRGB{0, 0, 0}, mid.AtLeast(mid, 21))
	assert.Equal(t, SRGB{255, 255, 255}, mid.AtLeast(SRGB{0, 0, 0}, 21))
}

func TestRampAscendingContrastSameHue(t *testing.T) {
	for _, bg := range []SRGB{{255, 255, 255}, {0x12, 0x12, 0x12}} {
		shades, err := mustHex("#2D9B55").ramp(bg, 5)
		assert.NoError(t, err)
		assert.Equal(t, 5, len(shades))
		prev := 0.0
		for _, s := range shades {
			c := s.Contrast(bg)
			assert.True(t, c > prev)
			prev = c
			d := s.OKLab().lch().H - mustHex("#2D9B55").OKLab().lch().H
			assert.True(t, d < 0.2 && d > -0.2)
		}
	}
}

func TestRampBounds(t *testing.T) {
	c := SRGB{1, 2, 3}
	_, err := c.ramp(SRGB{}, 0)
	assert.Error(t, err)
	_, err = c.ramp(SRGB{}, maxRampLevels+1)
	assert.Error(t, err)
	one, err := c.ramp(SRGB{}, 1)
	assert.NoError(t, err)
	assert.Equal(t, []SRGB{c}, one)
	_, err = c.ramp(SRGB{}, maxRampLevels)
	assert.NoError(t, err)
}

func TestQuieterKeepsTarget(t *testing.T) {
	bg, fg := SRGB{255, 255, 255}, mustHex("#232830")
	m := fg.Quieter(bg, ContrastText)
	assert.True(t, m.Contrast(bg) >= ContrastText)
	assert.True(t, m.Contrast(bg) < ContrastText+0.2)
	// Already quieter than the target: unchanged.
	assert.Equal(t, fg, fg.Quieter(bg, 100))
}
