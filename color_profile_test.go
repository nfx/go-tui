// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"strconv"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestEncodeTrueColor(t *testing.T) {
	c := sRGB{0x00, 0xAD, 0xD8}
	assert.Equal(t, "\x1b[38;2;0;173;216m", profileTrueColor.fg(c))
	assert.Equal(t, "\x1b[48;2;0;173;216m", profileTrueColor.bg(c))
}

func TestEncodeNoColorProfiles(t *testing.T) {
	c := sRGB{1, 2, 3}
	for _, p := range []colorProfile{profilePlain, profileAttrs} {
		assert.Equal(t, "", string(p.fg(c)))
		assert.Equal(t, "", string(p.bg(c)))
	}
	for _, p := range []colorProfile{profileANSI16, profileANSI256, profileTrueColor} {
		assert.True(t, len(p.fg(c)) > 0)
		assert.True(t, len(p.bg(c)) > 0)
	}
}

func TestEncodeExactPaletteHits(t *testing.T) {
	pal := ansiPalette()
	// Indexes 0..15 are the lowest in the 256 table, so exact hits land there.
	for i := range 16 {
		fgCode, bgCode := 30+i, 40+i
		if i >= 8 {
			fgCode, bgCode = 90+i-8, 100+i-8
		}
		assert.Equal(t, "\x1b["+strconv.Itoa(fgCode)+"m", profileANSI16.fg(pal[i]))
		assert.Equal(t, "\x1b["+strconv.Itoa(bgCode)+"m", profileANSI16.bg(pal[i]))
		assert.Equal(t, "\x1b[38;5;"+strconv.Itoa(i)+"m", profileANSI256.fg(pal[i]))
	}
	// Cube and gray entries that are not duplicates of 0..15.
	for _, i := range []int{17, 22, 52, 232, 255} {
		assert.Equal(t, "\x1b[38;5;"+strconv.Itoa(i)+"m", profileANSI256.fg(pal[i]))
	}
}

func TestAnsi16BrightCodesAvoidBold(t *testing.T) {
	for i, want := range map[int][2]string{
		0:  {"\x1b[30m", "\x1b[40m"},
		7:  {"\x1b[37m", "\x1b[47m"},
		8:  {"\x1b[90m", "\x1b[100m"},
		15: {"\x1b[97m", "\x1b[107m"},
	} {
		assert.Equal(t, want[0], ansiPalette()[i].ansi16Escape(false))
		assert.Equal(t, want[1], ansiPalette()[i].ansi16Escape(true))
	}
}

func TestResetsDoNotTouchAttributes(t *testing.T) {
	assert.Equal(t, "\x1b[39m", fgDefault)
	assert.Equal(t, "\x1b[49m", bgDefault)
}

func TestShownByProfile(t *testing.T) {
	c := sRGB{0x01, 0x02, 0xFE}
	assert.Equal(t, c, profileTrueColor.shown(c))
	assert.Equal(t, sRGB{0, 0, 0xFF}, profileANSI16.shown(c))
	assert.Equal(t, sRGB{0, 0, 0xFF}, profileANSI256.shown(c))
}

func TestReadableRechecksQuantizedContrast(t *testing.T) {
	white := sRGB{255, 255, 255}
	seed := RGB("#00ADD8").must()
	for _, p := range []colorProfile{profileANSI16, profileANSI256, profileTrueColor} {
		got, ok := p.readable(seed, white, contrastText)
		if ok {
			assert.True(t, p.shown(got).contrast(white) >= contrastText)
		}
	}
	got, ok := profileTrueColor.readable(seed, white, contrastText)
	assert.True(t, ok)
	assert.True(t, got.contrast(white) >= contrastText)
}

func TestReadableGivesUp(t *testing.T) {
	// Nothing in 16 colors has 21:1 against mid gray.
	mid := sRGB{0x77, 0x77, 0x77}
	_, ok := profileANSI16.readable(sRGB{0, 0xAA, 0}, mid, 21)
	assert.True(t, !ok)
}

func TestDistinctCollapsesByEncoding(t *testing.T) {
	shades, err := RGB("#2D9B55").must().ramp(sRGB{0x12, 0x12, 0x12}, 5)
	assert.NoError(t, err)
	assert.Equal(t, 5, len(profileTrueColor.distinct(shades)))
	assert.True(t, len(profileANSI16.distinct(shades)) < 5)
	assert.Equal(t, 1, len(profileAttrs.distinct(shades)))
	assert.Equal(t, shades[0], profileAttrs.distinct(shades)[0])

	// Different spellings of one palette entry collapse; compare encoding.
	a, b := sRGB{0x00, 0x7F, 0x00}, sRGB{0x00, 0x80, 0x00}
	assert.Equal(t, 1, len(profileANSI16.distinct([]sRGB{a, b})))
	assert.Equal(t, 2, len(profileTrueColor.distinct([]sRGB{a, b})))
}

func TestSGREffect(t *testing.T) {
	for _, tc := range []struct {
		seq          string
		set, cleared sgrChannel
	}{
		{"\x1b[m", 0, sgrAll},
		{"\x1b[0m", 0, sgrAll},
		{"\x1b[31m", sgrForeground, 0},
		{"\x1b[0;31m", sgrForeground, sgrAll &^ sgrForeground},
		{"\x1b[31;0m", 0, sgrAll},
		{"\x1b[1;22m", 0, sgrIntensity},
		{"\x1b[22m", 0, sgrIntensity},
		{"\x1b[23;24;29m", 0, sgrItalic | sgrUnderline | sgrStrike},
		{"\x1b[39;49m", 0, sgrForeground | sgrBackground},
		{"\x1b[38;5;0m", sgrForeground, 0},
		{"\x1b[38;2;0;0;0m", sgrForeground, 0},
		{"\x1b[48:2::0:0:0m", sgrBackground, 0},
		{"\x1b[58;5;0;1m", sgrIntensity, 0},
		{"\x1b[97;104m", sgrForeground | sgrBackground, 0},
		{"\x1b[99999999999999999999m", 0, 0},
		{"\x1b[2K", 0, 0},
		{"\x1b[>4m", 0, 0},
	} {
		set, cleared := text(tc.seq).scan().sgrEffect()
		if set != tc.set || cleared != tc.cleared {
			t.Errorf("%q: got set %06b cleared %06b, want %06b %06b", tc.seq, set, cleared, tc.set, tc.cleared)
		}
	}
}
