// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package color

import (
	"github.com/nfx/go-tui/internal/ansi"
	"strconv"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestEncodeTrueColor(t *testing.T) {
	c := SRGB{0x00, 0xAD, 0xD8}
	assert.Equal(t, "\x1b[38;2;0;173;216m", ProfileTrueColor.Fg(c))
	assert.Equal(t, "\x1b[48;2;0;173;216m", ProfileTrueColor.bg(c))
}

func TestEncodeNoColorProfiles(t *testing.T) {
	c := SRGB{1, 2, 3}
	for _, p := range []Profile{ProfilePlain, ProfileAttrs} {
		assert.Equal(t, "", string(p.Fg(c)))
		assert.Equal(t, "", string(p.bg(c)))
	}
	for _, p := range []Profile{ProfileANSI16, ProfileANSI256, ProfileTrueColor} {
		assert.True(t, len(p.Fg(c)) > 0)
		assert.True(t, len(p.bg(c)) > 0)
	}
}

func TestEncodeExactPaletteHits(t *testing.T) {
	pal := ANSIPalette()
	// Indexes 0..15 are the lowest in the 256 table, so exact hits land there.
	for i := range 16 {
		fgCode, bgCode := 30+i, 40+i
		if i >= 8 {
			fgCode, bgCode = 90+i-8, 100+i-8
		}
		assert.Equal(t, "\x1b["+strconv.Itoa(fgCode)+"m", ProfileANSI16.Fg(pal[i]))
		assert.Equal(t, "\x1b["+strconv.Itoa(bgCode)+"m", ProfileANSI16.bg(pal[i]))
		assert.Equal(t, "\x1b[38;5;"+strconv.Itoa(i)+"m", ProfileANSI256.Fg(pal[i]))
	}
	// Cube and gray entries that are not duplicates of 0..15.
	for _, i := range []int{17, 22, 52, 232, 255} {
		assert.Equal(t, "\x1b[38;5;"+strconv.Itoa(i)+"m", ProfileANSI256.Fg(pal[i]))
	}
}

func TestAnsi16BrightCodesAvoidBold(t *testing.T) {
	for i, want := range map[int][2]string{
		0:  {"\x1b[30m", "\x1b[40m"},
		7:  {"\x1b[37m", "\x1b[47m"},
		8:  {"\x1b[90m", "\x1b[100m"},
		15: {"\x1b[97m", "\x1b[107m"},
	} {
		assert.Equal(t, want[0], ANSIPalette()[i].ansi16Escape(false))
		assert.Equal(t, want[1], ANSIPalette()[i].ansi16Escape(true))
	}
}

func TestResetsDoNotTouchAttributes(t *testing.T) {
	assert.Equal(t, "\x1b[39m", FgDefault)
	assert.Equal(t, "\x1b[49m", BgDefault)
}

func TestShownByProfile(t *testing.T) {
	c := SRGB{0x01, 0x02, 0xFE}
	assert.Equal(t, c, ProfileTrueColor.Shown(c))
	assert.Equal(t, SRGB{0, 0, 0xFF}, ProfileANSI16.Shown(c))
	assert.Equal(t, SRGB{0, 0, 0xFF}, ProfileANSI256.Shown(c))
}

func TestReadableRechecksQuantizedContrast(t *testing.T) {
	white := SRGB{255, 255, 255}
	seed := mustHex("#00ADD8")
	for _, p := range []Profile{ProfileANSI16, ProfileANSI256, ProfileTrueColor} {
		got, ok := p.Readable(seed, white, ContrastText)
		if ok {
			assert.True(t, p.Shown(got).Contrast(white) >= ContrastText)
		}
	}
	got, ok := ProfileTrueColor.Readable(seed, white, ContrastText)
	assert.True(t, ok)
	assert.True(t, got.Contrast(white) >= ContrastText)
}

func TestReadableGivesUp(t *testing.T) {
	// Nothing in 16 colors has 21:1 against mid gray.
	mid := SRGB{0x77, 0x77, 0x77}
	_, ok := ProfileANSI16.Readable(SRGB{0, 0xAA, 0}, mid, 21)
	assert.True(t, !ok)
}

func TestDistinctCollapsesByEncoding(t *testing.T) {
	shades, err := mustHex("#2D9B55").ramp(SRGB{0x12, 0x12, 0x12}, 5)
	assert.NoError(t, err)
	assert.Equal(t, 5, len(ProfileTrueColor.distinct(shades)))
	assert.True(t, len(ProfileANSI16.distinct(shades)) < 5)
	assert.Equal(t, 1, len(ProfileAttrs.distinct(shades)))
	assert.Equal(t, shades[0], ProfileAttrs.distinct(shades)[0])

	// Different spellings of one palette entry collapse; compare encoding.
	a, b := SRGB{0x00, 0x7F, 0x00}, SRGB{0x00, 0x80, 0x00}
	assert.Equal(t, 1, len(ProfileANSI16.distinct([]SRGB{a, b})))
	assert.Equal(t, 2, len(ProfileTrueColor.distinct([]SRGB{a, b})))
}

func TestSGREffect(t *testing.T) {
	for _, tc := range []struct {
		seq          string
		set, cleared ansi.SGRChannel
	}{
		{"\x1b[m", 0, ansi.SGRAll},
		{"\x1b[0m", 0, ansi.SGRAll},
		{"\x1b[31m", ansi.SGRForeground, 0},
		{"\x1b[0;31m", ansi.SGRForeground, ansi.SGRAll &^ ansi.SGRForeground},
		{"\x1b[31;0m", 0, ansi.SGRAll},
		{"\x1b[1;22m", 0, ansi.SGRIntensity},
		{"\x1b[22m", 0, ansi.SGRIntensity},
		{"\x1b[23;24;29m", 0, ansi.SGRItalic | ansi.SGRUnderline | ansi.SGRStrike},
		{"\x1b[39;49m", 0, ansi.SGRForeground | ansi.SGRBackground},
		{"\x1b[38;5;0m", ansi.SGRForeground, 0},
		{"\x1b[38;2;0;0;0m", ansi.SGRForeground, 0},
		{"\x1b[48:2::0:0:0m", ansi.SGRBackground, 0},
		{"\x1b[58;5;0;1m", ansi.SGRIntensity, 0},
		{"\x1b[97;104m", ansi.SGRForeground | ansi.SGRBackground, 0},
		{"\x1b[99999999999999999999m", 0, 0},
		{"\x1b[2K", 0, 0},
		{"\x1b[>4m", 0, 0},
	} {
		set, cleared := ansi.Text(tc.seq).Scan().SGREffect()
		if set != tc.set || cleared != tc.cleared {
			t.Errorf("%q: got set %06b cleared %06b, want %06b %06b", tc.seq, set, cleared, tc.set, tc.cleared)
		}
	}
}
