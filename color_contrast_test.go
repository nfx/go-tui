// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"math"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestOKLabReferenceValues(t *testing.T) {
	assert.Equal(t, oklab{}, sRGB{0, 0, 0}.oklab())
	white := sRGB{255, 255, 255}.oklab()
	assert.True(t, white.distance(oklab{L: 1}) <= 1e-6)
	// Ottosson's published reference: pure red is about (0.628, 0.225, 0.126).
	red := sRGB{255, 0, 0}.oklab()
	assert.True(t, red.distance(oklab{L: 0.6280, A: 0.2249, B: 0.1258}) <= 1e-5)
}

func TestContrastReferenceValues(t *testing.T) {
	black, white := sRGB{0, 0, 0}, sRGB{255, 255, 255}
	assert.True(t, black.contrast(white) > 20.99 && black.contrast(white) <= 21)
	assert.Equal(t, 1.0, white.contrast(white))
	assert.Equal(t, white.contrast(black), black.contrast(white))
	assert.True(t, black.contrast(sRGB{0x77, 0x77, 0x77}) > 4.6)
}

func TestLightBoundaryEqualisesBlackAndWhite(t *testing.T) {
	// A background exactly at the boundary has equal contrast to both.
	y := lightBoundary
	assert.True(t, math.Abs((1.05)/(y+0.05)-(y+0.05)/0.05) < 1e-6)
	assert.True(t, sRGB{255, 255, 255}.isLight())
	assert.True(t, !sRGB{0, 0, 0}.isLight())
	assert.True(t, !sRGB{0x12, 0x12, 0x12}.isLight())
	assert.True(t, sRGB{0x80, 0x80, 0x80}.isLight())
	assert.True(t, !sRGB{0, 0, 0x80}.isLight()) // dark blue
}

func TestOKLabRoundTrip(t *testing.T) {
	for r := 0; r < 256; r += 51 {
		for g := 0; g < 256; g += 51 {
			for b := 0; b < 256; b += 51 {
				c := sRGB{uint8(r), uint8(g), uint8(b)}
				back, ok := c.oklab().srgb()
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
	back := got.oklab().lch()
	assert.True(t, back.C < lch.C)
	d := back.H - lch.H
	assert.True(t, d < 0.1 && d > -0.1)
}

func TestSeedKeptWhenReadable(t *testing.T) {
	accent := RGB("#00ADD8").must()
	// Bright cyan is readable on a dark background: rendered literally.
	assert.Equal(t, accent, accent.atLeast(sRGB{0x12, 0x12, 0x12}, contrastText))
	// But not on white.
	got := accent.atLeast(sRGB{255, 255, 255}, contrastText)
	assert.True(t, got != accent)
	assert.True(t, got.contrast(sRGB{255, 255, 255}) >= contrastText)
	assert.True(t, got.luminance() < accent.luminance())
}

func TestAtLeastIsSmallestStep(t *testing.T) {
	white := sRGB{255, 255, 255}
	got := RGB("#00ADD8").must().atLeast(white, contrastText)
	assert.True(t, got.contrast(white) < contrastText+0.15)
}

func TestAtLeastPicksNearerDirection(t *testing.T) {
	// on a mid-gray background, a dark seed is closer to passing by darkening
	seed, bg := RGB("#303030").must(), RGB("#606060").must()
	got := seed.atLeast(bg, contrastMark)
	assert.True(t, got.contrast(bg) >= contrastMark)
	assert.True(t, got.luminance() < bg.luminance())
	assert.True(t, math.Abs(got.oklab().L-seed.oklab().L) < 0.2)
	// and a light seed is closer by lightening
	light := RGB("#C0C0C0").must().atLeast(bg, contrastMark)
	assert.True(t, light.luminance() > bg.luminance())
}

func TestAtLeastKeepsHue(t *testing.T) {
	seed := RGB("#C83741").must()
	got := seed.atLeast(sRGB{0, 0, 0}, 9)
	d := got.oklab().lch().H - seed.oklab().lch().H
	assert.True(t, d < 0.15 && d > -0.15)
	assert.True(t, got.contrast(sRGB{0, 0, 0}) >= 9)
}

func TestAtLeastUnreachableReturnsExtreme(t *testing.T) {
	mid := sRGB{0x77, 0x77, 0x77}
	assert.Equal(t, sRGB{0, 0, 0}, mid.atLeast(mid, 21))
	assert.Equal(t, sRGB{255, 255, 255}, mid.atLeast(sRGB{0, 0, 0}, 21))
}

func TestRampAscendingContrastSameHue(t *testing.T) {
	for _, bg := range []sRGB{{255, 255, 255}, {0x12, 0x12, 0x12}} {
		shades, err := RGB("#2D9B55").must().ramp(bg, 5)
		assert.NoError(t, err)
		assert.Equal(t, 5, len(shades))
		prev := 0.0
		for _, s := range shades {
			c := s.contrast(bg)
			assert.True(t, c > prev)
			prev = c
			d := s.oklab().lch().H - RGB("#2D9B55").must().oklab().lch().H
			assert.True(t, d < 0.2 && d > -0.2)
		}
	}
}

func TestRampBounds(t *testing.T) {
	c := sRGB{1, 2, 3}
	_, err := c.ramp(sRGB{}, 0)
	assert.Error(t, err)
	_, err = c.ramp(sRGB{}, maxRampLevels+1)
	assert.Error(t, err)
	one, err := c.ramp(sRGB{}, 1)
	assert.NoError(t, err)
	assert.Equal(t, []sRGB{c}, one)
	_, err = c.ramp(sRGB{}, maxRampLevels)
	assert.NoError(t, err)
}

func TestQuieterKeepsTarget(t *testing.T) {
	bg, fg := sRGB{255, 255, 255}, RGB("#232830").must()
	m := fg.quieter(bg, contrastText)
	assert.True(t, m.contrast(bg) >= contrastText)
	assert.True(t, m.contrast(bg) < contrastText+0.2)
	// Already quieter than the target: unchanged.
	assert.Equal(t, fg, fg.quieter(bg, 100))
}
