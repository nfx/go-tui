// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestSRGBHexIsCanonical(t *testing.T) {
	for _, in := range []string{"#0ad", "#00aadd", "#00AADD"} {
		c, err := parseHex(in)
		assert.NoError(t, err)
		assert.Equal(t, "#00AADD", c.hex())
	}
}

func TestAnsiPaletteLayout(t *testing.T) {
	p := ansiPalette()
	for i, want := range map[int]sRGB{
		0: {0, 0, 0}, 1: {0x80, 0, 0}, 7: {0xc0, 0xc0, 0xc0}, 8: {0x80, 0x80, 0x80},
		9: {255, 0, 0}, 15: {255, 255, 255},
		16: {0, 0, 0}, 17: {0, 0, 95}, 21: {0, 0, 255}, 22: {0, 95, 0},
		52: {95, 0, 0}, 231: {255, 255, 255},
		232: {8, 8, 8}, 255: {238, 238, 238},
	} {
		assert.Equal(t, want, p[i])
	}
}

func TestNearestIndexGrayscaleAndCubeEdges(t *testing.T) {
	for _, tc := range []struct {
		in   sRGB
		n    int
		want int
	}{
		{sRGB{0, 0, 0}, 256, 0},         // duplicate of cube 16: lowest index wins
		{sRGB{255, 255, 255}, 256, 15},  // duplicate of cube 231
		{sRGB{8, 8, 8}, 256, 232},       // first gray
		{sRGB{238, 238, 238}, 256, 255}, // last gray
		{sRGB{95, 0, 0}, 256, 52},
		{sRGB{255, 0, 0}, 256, 9},
		{sRGB{0, 0, 255}, 256, 12},
		{sRGB{250, 250, 250}, 256, 15},
		{sRGB{250, 250, 250}, 16, 15},
		{sRGB{10, 10, 10}, 256, 232},
	} {
		assert.Equal(t, tc.want, tc.in.nearestIndex(tc.n))
	}
}

func TestBasicIndexKeepsGraysAndHuesApart(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"#00ADD8", 6},  // the accent: teal, though gray is nearer
		{"#3E4042", 8},  // the dark guide: gray, though maroon is nearer
		{"#4D4E50", 8},  // gray, though teal is nearer
		{"#708090", 8},  // slate gray
		{"#8B4513", 1},  // saddle brown
		{"#FAFAFA", 15}, // near white
	} {
		c, err := parseHex(tc.in)
		assert.NoError(t, err)
		assert.Equal(t, tc.want, c.basicIndex())
	}
}

func TestNearestIndexStableTies(t *testing.T) {
	for range 3 {
		assert.Equal(t, 0, sRGB{0, 0, 0}.nearestIndex(256))
	}
	// A color equidistant from identical candidates never picks the later one.
	pal := ansiPalette()
	for i := 16; i < 256; i++ {
		got := pal[i].nearestIndex(256)
		assert.True(t, got <= i)
		assert.Equal(t, pal[i], pal[got])
	}
}

func TestDirectSixteenBeatsCascading(t *testing.T) {
	pal := ansiPalette()
	lab := ansiPaletteLab()
	differs := false
	for r := 0; r < 256; r += 15 {
		for g := 0; g < 256; g += 15 {
			for b := 0; b < 256; b += 15 {
				c := sRGB{uint8(r), uint8(g), uint8(b)}
				target := c.oklab()
				direct := c.nearestIndex(16)
				cascade := pal[c.nearestIndex(256)].nearestIndex(16)
				assert.True(t, target.distance(lab[direct]) <= target.distance(lab[cascade]))
				if direct != cascade {
					differs = true
				}
			}
		}
	}
	// At least one color where cascading picks a worse entry.
	assert.True(t, differs)
}

func TestParseHexRejectsMalformed(t *testing.T) {
	for _, in := range []string{"", "00ADD8", "#00ADD", "#GG0000", "#ÄÄÄ"} {
		_, err := parseHex(in)
		assert.Error(t, err)
	}
	assert.Equal(t, sRGB{0, 0xAA, 0xDD}, mustHex("#0aD"))
}
