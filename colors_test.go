// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestSpanHelperHandlesTypes(t *testing.T) {
	str := "hi"
	ptr := &str
	arr := []string{"alpha", "beta"}
	num := 5
	formatter := destination{}.newSpan(reset, text("\x1b[31m"), text("\x1b[1m")).helper()

	got := formatter(ptr, arr, &num, "end")
	if !strings.HasPrefix(got, "\x1b[31m\x1b[1m") {
		t.Fatalf("expected prefix codes, got %q", got)
	}
	if !strings.Contains(got, "alpha, beta") {
		t.Fatalf("expected joined array, got %q", got)
	}
	if !strings.Contains(got, "5") {
		t.Fatalf("expected pointer value, got %q", got)
	}
	if !strings.HasSuffix(got, reset) {
		t.Fatalf("expected reset suffix, got %q", got)
	}
}

func TestRGBParse(t *testing.T) {
	for _, tc := range []struct {
		in   RGB
		want sRGB
	}{
		{"#00ADD8", sRGB{0x00, 0xAD, 0xD8}},
		{"#00add8", sRGB{0x00, 0xAD, 0xD8}},
		{"#0AD", sRGB{0x00, 0xAA, 0xDD}},
		{"#0ad", sRGB{0x00, 0xAA, 0xDD}},
		{"#000000", sRGB{}},
		{"#FFF", sRGB{255, 255, 255}},
	} {
		t.Run(string(tc.in), func(t *testing.T) {
			got, err := tc.in.parse()
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.NoError(t, tc.in.Validate())
		})
	}
}

func TestRGBEmptyIsUnset(t *testing.T) {
	got, err := RGB("").parse()
	assert.NoError(t, err)
	assert.Equal(t, sRGB{}, got)
	assert.NoError(t, RGB("").Validate())
}

func TestRGBRejects(t *testing.T) {
	for _, in := range []RGB{
		"00ADD8", "#00ADD", "#00ADD8FF", "#0ADF", "#", "#GG0000", "#00ADDG",
		" #00ADD8", "#00ADD8 ", "teal", "rgb(0,173,216)", "hsl(0,0%,0%)", "#00AD D8", "#ÄÄÄ",
	} {
		t.Run(string(in), func(t *testing.T) {
			err := in.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), `want "#RRGGBB" or "#RGB"`)
		})
	}
}

func TestSRGBHexIsCanonical(t *testing.T) {
	for _, in := range []RGB{"#0ad", "#00aadd", "#00AADD"} {
		c, err := in.parse()
		assert.NoError(t, err)
		assert.Equal(t, RGB("#00AADD"), c.hex())
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

func TestOKLabReferenceValues(t *testing.T) {
	assert.Equal(t, oklab{}, sRGB{0, 0, 0}.oklab())
	white := sRGB{255, 255, 255}.oklab()
	assert.True(t, white.distance(oklab{L: 1}) <= 1e-6)
	// Ottosson's published reference: pure red is about (0.628, 0.225, 0.126).
	red := sRGB{255, 0, 0}.oklab()
	assert.True(t, red.distance(oklab{L: 0.6280, A: 0.2249, B: 0.1258}) <= 1e-5)
}

func TestResetsDoNotTouchAttributes(t *testing.T) {
	assert.Equal(t, "\x1b[39m", fgDefault)
	assert.Equal(t, "\x1b[49m", bgDefault)
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

func TestResolveDefaultsBothAppearances(t *testing.T) {
	for _, light := range []bool{false, true} {
		r, err := Palette{}.resolve(light, sRGB{}, false)
		assert.NoError(t, err)
		assert.Equal(t, light, r.Light)
		assert.True(t, !r.Known)
		bg := r.Background
		assert.True(t, r.Foreground.contrast(bg) >= contrastText)
		assert.True(t, r.Muted.contrast(bg) >= contrastText)
		assert.True(t, r.Guide.contrast(bg) >= contrastGuide)
		assert.True(t, r.Accent.contrast(bg) >= contrastText)
		assert.True(t, r.Success.contrast(bg) >= contrastMark)
		assert.True(t, r.Error.contrast(bg) >= contrastText)
		assert.Equal(t, r.Guide, r.Track)
		// Muted is quieter than the foreground, guide quieter than muted.
		assert.True(t, r.Muted.contrast(bg) <= r.Foreground.contrast(bg))
		assert.True(t, r.Guide.contrast(bg) <= r.Muted.contrast(bg))
	}
}

func TestResolveMeasuredBackgroundPicksVariant(t *testing.T) {
	r, err := Palette{}.resolve(false, sRGB{250, 250, 250}, true)
	assert.NoError(t, err)
	assert.True(t, r.Light)
	assert.True(t, r.Known)
	assert.Equal(t, sRGB{250, 250, 250}, r.Background)
	assert.Equal(t, RGB("#232830").must(), r.Foreground)
	// A seed in the preset moves on a light terminal but not on a dark one.
	dark, err := Palette{}.resolve(true, sRGB{0, 0, 0}, true)
	assert.NoError(t, err)
	assert.Equal(t, RGB("#00ADD8").must(), dark.Accent)
	assert.True(t, r.Accent != dark.Accent)
}

func TestResolveDefaultNeutralsReadableAroundBoundary(t *testing.T) {
	for _, v := range []uint8{0x60, 0x70, 0x77, 0x80, 0x90, 0xA0} {
		bg := sRGB{v, v, v}
		r, err := Palette{}.resolve(false, bg, true)
		assert.NoError(t, err)
		assert.True(t, r.Foreground.contrast(bg) >= contrastText-0.2)
		assert.True(t, r.Muted.contrast(bg) >= contrastText-0.2)
		assert.True(t, r.Accent.contrast(bg) >= contrastText-0.2)
	}
}

func TestResolveExplicitForegroundIsLiteralOnAnyBackground(t *testing.T) {
	bg := sRGB{0x77, 0x77, 0x77}
	r, err := Palette{
		Light: PaletteVariant{Foreground: "#232830"},
		Dark:  PaletteVariant{Foreground: "#232830"},
	}.resolve(
		false,
		bg,
		true,
	)
	assert.NoError(t, err)
	assert.Equal(t, RGB("#232830").must(), r.Foreground)
}

func TestResolveOverrideIsLiteralAndSeedUnchanged(t *testing.T) {
	p := Palette{Accent: "#00ADD8"}
	p.Light.Accent = "#00ADD8" // deliberately low contrast on white
	p.Light.Muted = "#CCCCCC"
	r, err := p.resolve(true, sRGB{}, false)
	assert.NoError(t, err)
	assert.Equal(t, RGB("#00ADD8").must(), r.Accent)
	assert.Equal(t, RGB("#CCCCCC").must(), r.Muted)
	assert.Equal(t, RGB("#00ADD8"), p.Accent)
	assert.Equal(t, RGB(""), p.Light.Foreground) // caller value never filled in
}

func TestResolveEquivalentSpellingsMatch(t *testing.T) {
	a, err := Palette{Accent: "#0AD"}.resolve(true, sRGB{}, false)
	assert.NoError(t, err)
	b, err := Palette{Accent: "#00aadd"}.resolve(true, sRGB{}, false)
	assert.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestResolveRejectsMalformedWithPath(t *testing.T) {
	p := Palette{}
	p.Dark.Accent = "00ADD"
	_, err := p.resolve(false, sRGB{}, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Palette.Dark.Accent")
	assert.Contains(t, err.Error(), `"00ADD"`)

	_, err = Palette{Error: "red"}.resolve(false, sRGB{}, false)
	assert.Contains(t, err.Error(), "theme.Palette.Error")
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

func TestPresetsCompile(t *testing.T) {
	for name, th := range map[string]Theme{"builtin": NewTheme()} {
		t.Run(name, func(t *testing.T) {
			_, err := th.compile()
			assert.NoError(t, err)
		})
	}
}

func TestSymbolPresetsAreOneColumn(t *testing.T) {
	_, err := Symbols{}.normalize()
	assert.NoError(t, err)
	_, err = ASCIISymbols().normalize()
	assert.NoError(t, err)
}

func TestZeroThemeCompiles(t *testing.T) {
	_, err := Theme{}.compile()
	assert.NoError(t, err)
}

func TestPresetKeepsAdaptiveNeutralsUnset(t *testing.T) {
	th := NewTheme()
	assert.Equal(t, RGB(""), th.Palette.Light.Foreground)
	assert.Equal(t, RGB(""), th.Palette.Dark.Foreground)
	c, err := th.compile()
	assert.NoError(t, err)
	assert.True(t, !c.palette.dark.foreground.set)
	assert.True(t, !c.palette.light.muted.set)
}

func TestRoundTripKeepsIntentAndCanonicalizes(t *testing.T) {
	th := NewTheme()
	th.Palette.Accent = "#0ad"
	th.Palette.Dark.Error = "#000"
	c, err := th.compile()
	assert.NoError(t, err)
	got := c.editable()
	assert.Equal(t, RGB("#00AADD"), got.Palette.Accent)
	assert.Equal(t, RGB("#000000"), got.Palette.Dark.Error) // explicit black stays explicit
	assert.Equal(t, RGB(""), got.Palette.Dark.Foreground)
	again, err := got.compile()
	assert.NoError(t, err)
	assert.True(t, again.palette.dark.err.set)
	assert.True(t, !again.palette.dark.foreground.set)
	assert.Equal(t, RGB("#0ad"), th.Palette.Accent) // caller value untouched
}

func TestPaletteErrorIsPathQualified(t *testing.T) {
	th := NewTheme()
	th.Palette.Dark.Accent = "00ADD8"
	_, err := th.compile()
	assert.Contains(t, err.Error(), "theme.Palette.Dark.Accent")
}

func TestInstallAtomicAndIsolation(t *testing.T) {
	prev := defaultIO.currentTheme()
	t.Cleanup(func() { defaultIO.theme.Store(prev) })

	th := NewTheme()
	th.Palette.Accent = "#112233"
	assert.NoError(t, defaultIO.installTheme(th))
	c := defaultIO.currentTheme()
	assert.Equal(t, RGB("#112233"), c.editable().Palette.Accent)

	// Mutating the caller's value and the returned copy leaves the snapshot alone.
	th.Palette.Accent = "#445566"
	got := defaultIO.currentTheme().editable()
	got.Palette.Accent = "#778899"
	assert.Equal(t, RGB("#112233"), c.source.Palette.Accent)
	assert.Equal(t, RGB("#112233"), defaultIO.currentTheme().editable().Palette.Accent)

	// A failed installation changes nothing.
	bad := NewTheme()
	bad.Palette.Accent = "nope"
	assert.Error(t, defaultIO.installTheme(bad))
	assert.True(t, defaultIO.currentTheme() == c)
}

func TestConcurrentInstall(t *testing.T) {
	prev := defaultIO.currentTheme()
	t.Cleanup(func() { defaultIO.theme.Store(prev) })
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			th := NewTheme()
			if i%2 == 0 {
				th.Palette.Accent = "nope"
			}
			err := defaultIO.installTheme(th)
			if i%2 == 0 {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			if c := defaultIO.currentTheme(); c == nil {
				t.Error("torn theme")
			}
		})
	}
	wg.Wait()
}

func TestParsedPaletteResolveMatchesAdapter(t *testing.T) {
	p := Palette{Accent: "#00ADD8", Dark: PaletteVariant{Muted: "#777777"}}
	want, err := p.resolve(false, sRGB{0x20, 0x20, 0x20}, true)
	assert.NoError(t, err)
	pp, err := p.parse()
	assert.NoError(t, err)
	assert.Equal(t, want, pp.resolve(false, sRGB{0x20, 0x20, 0x20}, true))
}

// compileTemplates compiles the built-in theme with test-specific edits.
func compileTemplates(t *testing.T, edit func(*Theme)) (*compiledTheme, error) {
	t.Helper()
	th := NewTheme()
	edit(&th)
	return th.compile()
}

func TestTemplateZeroInheritsAndEmptyHides(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Prompt.Label = Template("{{ bold .Label }}")
		th.Templates.Dropdown.Hint = Hidden
	})
	if err != nil {
		t.Fatal(err)
	}
	tt := c.templates
	for p, s := range map[string]*compiledSlot{
		"Prompt": tt.Prompt.Label, "Input": tt.Input.Label, "Password": tt.Password.Label,
		"Number": tt.Number.Label, "Dropdown": tt.Dropdown.Label, "Multichoice": tt.Multichoice.Label,
		"Confirm": tt.Confirm.Label, "FilePicker": tt.FilePicker.Label,
	} {
		if s == nil || s.tmpl == nil {
			t.Errorf("%s.Label did not inherit the shared label", p)
		}
	}
	if s := tt.Dropdown.Hint; s == nil || !s.hidden {
		t.Error("explicit empty did not hide Dropdown.Hint")
	}
	if s := tt.Multichoice.Hint; s == nil || s != tt.Prompt.Hint {
		t.Error("unset slot must fall to the built-in default")
	}
}

func TestTemplateFamilyOverrideStaysLocal(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Prompt.Label = Template("shared")
		th.Templates.Input.Label = Template("input")
	})
	if err != nil {
		t.Fatal(err)
	}
	tt := c.templates
	for _, tc := range []struct {
		path string
		slot *compiledSlot
		want string
	}{
		{"Prompt.Label", tt.Prompt.Label, "shared"},
		{"Input.Label", tt.Input.Label, "input"},
		{"Password.Label", tt.Password.Label, "input"},
		{"Number.Label", tt.Number.Label, "input"},
		{"Dropdown.Label", tt.Dropdown.Label, "shared"},
	} {
		if got := tc.slot.tmpl.Root.String(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestTemplateRequiredSlotRejectsEmpty(t *testing.T) {
	for _, path := range []string{"Choice.Active", "Choice.Inactive", "Choice.Value", "Multichoice.Row"} {
		_, err := compileTemplates(t, func(th *Theme) {
			switch path {
			case "Choice.Active":
				th.Templates.Choice.Active = Hidden
			case "Choice.Inactive":
				th.Templates.Choice.Inactive = Hidden
			case "Choice.Value":
				th.Templates.Choice.Value = Hidden
			default:
				th.Templates.Multichoice.Row = Hidden
			}
		})
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: want path-qualified error, got %v", path, err)
		}
	}
}

func TestTemplateErrorNamesDefiningSlot(t *testing.T) {
	_, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Prompt.Answer = Template("{{ .Value ")
	})
	if err == nil || !strings.Contains(err.Error(), "theme.Templates.Prompt.Answer") {
		t.Fatalf("got %v", err)
	}
	_, err = compileTemplates(t, func(th *Theme) {
		th.Templates.Input.Label = Template("{{ nosuchhelper .Label }}")
	})
	if err == nil || !strings.Contains(err.Error(), "theme.Templates.Input.Label") {
		t.Fatalf("unknown helper: got %v", err)
	}
}

func TestTemplateDefineIsIsolatedPerSlot(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Prompt.Label = Template(`{{ define "x" }}A{{ end }}{{ template "x" }}`)
		th.Templates.Prompt.Hint = Template(`{{ define "x" }}B{{ end }}{{ template "x" }}`)
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(s *compiledSlot) string {
		var b strings.Builder
		if err := s.tmpl.Execute(&b, nil); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if a, b := exec(c.templates.Prompt.Label), exec(c.templates.Prompt.Hint); a != "A" || b != "B" {
		t.Fatalf("define leaked between slots: %q %q", a, b)
	}
}

func TestThemeRoundTripKeepsTemplateSources(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Templates.Choice.More = Hidden })
	if err != nil {
		t.Fatal(err)
	}
	if got := c.editable().Templates.Choice.More; got != Hidden {
		t.Fatalf("explicit empty lost in round trip: %+v", got)
	}
	if c.editable().Templates.Choice.Empty != "" {
		t.Fatal("inherited slot became explicit")
	}
}

// keepTheme restores the installed theme after a test.
func keepTheme(t *testing.T) *compiledTheme {
	t.Helper()
	prev := defaultIO.currentTheme()
	t.Cleanup(func() { defaultIO.theme.Store(prev) })
	return prev
}

// execute renders src with funcs and fails the test on any error.
func execute(t *testing.T, funcs template.FuncMap, src string, data any) string {
	t.Helper()
	tmpl, err := template.New("t").Funcs(funcs).Parse(src)
	assert.NoError(t, err)
	var b strings.Builder
	assert.NoError(t, tmpl.Execute(&b, data))
	return b.String()
}

// installedFuncs are the helpers of the installed theme, for a plain destination.
func installedFuncs() template.FuncMap {
	return defaultIO.currentTheme().funcs(profilePlain, roleStyles{})
}

func TestWithFnRegistersForFutureOperations(t *testing.T) {
	before := keepTheme(t)
	WithFn("shout", func(s string) string { return strings.ToUpper(s) + "!" })

	assert.Equal(t, "HI!", execute(t, installedFuncs(), `{{ shout "hi" }}`, nil))
	_, ok := before.funcs(profilePlain, roleStyles{})["shout"]
	assert.True(t, !ok) // an operation that started earlier keeps its helpers
	_, ok = defaultIO.currentTheme().editable().Funcs["shout"]
	assert.True(t, ok)

	WithFn("shout", func(s string) string { return s + "?" })
	assert.Equal(t, "hi?", execute(t, installedFuncs(), `{{ shout "hi" }}`, nil))
}

func TestWithFnReachesWidgetTemplates(t *testing.T) {
	keepTheme(t)
	WithFn("shout", strings.ToUpper)
	funcs := installedFuncs()
	assert.Equal(t, "A", execute(t, funcs, `{{ shout "a" }}`, nil))
	assert.Equal(t, "x", execute(t, funcs, `{{ label "x" }}`, nil))
}

func TestWithFnPanicsAndKeepsInstalled(t *testing.T) {
	keepTheme(t)
	for name, fn := range map[string]any{
		"red":                    strings.ToUpper,
		"fg":                     strings.ToUpper,
		"accent":                 strings.ToUpper,
		"symbol":                 strings.ToUpper,
		"label":                  strings.ToUpper,
		"ago":                    strings.ToUpper,
		"tableString":            strings.ToUpper,
		"__cellSep":              strings.ToUpper,
		"tableColumnTypeFormat0": strings.ToUpper,
		"1st":                    strings.ToUpper,
		"a-b":                    strings.ToUpper,
		"":                       strings.ToUpper,
		"notFunc":                "x",
		"nilFunc":                (func() string)(nil),
		"untyped":                nil,
		"noResult":               func() {},
		"badSecond":              func() (string, string) { return "", "" },
		"threeResults":           func() (int, int, error) { return 0, 0, nil },
	} {
		t.Run(name, func(t *testing.T) {
			installed := defaultIO.currentTheme()
			defer func() {
				r := recover()
				err, ok := r.(error)
				if !ok {
					t.Fatalf("want a panic with an error, got %v", r)
				}
				assert.Contains(t, err.Error(), "tui.WithFn: theme.Funcs."+name+":")
				assert.True(t, defaultIO.currentTheme() == installed)
			}()
			WithFn(name, fn)
		})
	}
}

func TestWithFnAcceptsErrorResult(t *testing.T) {
	keepTheme(t)
	boom := errors.New("boom")
	WithFn("fails", func() (string, error) { return "", boom })
	tmpl, err := template.New("t").Funcs(installedFuncs()).Parse(`{{ fails }}`)
	assert.NoError(t, err)
	assert.ErrorIs(t, tmpl.Execute(&strings.Builder{}, nil), boom)
}

func TestConcurrentWithFnLosesNothing(t *testing.T) {
	keepTheme(t)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() { WithFn(fmt.Sprintf("fn%d", i), strings.ToUpper) })
	}
	wg.Wait()
	funcs := defaultIO.currentTheme().editable().Funcs
	for i := range 16 {
		_, ok := funcs[fmt.Sprintf("fn%d", i)]
		assert.True(t, ok)
	}
}

func TestInstallReplacesFuncs(t *testing.T) {
	keepTheme(t)
	WithFn("forgotten", strings.ToUpper)
	th := NewTheme()
	th.Funcs = template.FuncMap{"kept": strings.ToLower}
	assert.NoError(t, defaultIO.installTheme(th))
	funcs := defaultIO.currentTheme().editable().Funcs
	_, forgotten := funcs["forgotten"]
	_, kept := funcs["kept"]
	assert.True(t, !forgotten && kept)
}

func TestThemeFuncsAreCopied(t *testing.T) {
	keepTheme(t)
	th := NewTheme()
	th.Funcs = template.FuncMap{"h": strings.ToUpper}
	assert.NoError(t, defaultIO.installTheme(th))
	th.Funcs["h"] = strings.ToLower
	th.Funcs["extra"] = strings.ToLower
	got := defaultIO.currentTheme().editable()
	got.Funcs["h"] = strings.ToLower
	assert.Equal(t, "A", execute(t, installedFuncs(), `{{ h "a" }}`, nil))
	_, ok := defaultIO.currentTheme().editable().Funcs["extra"]
	assert.True(t, !ok)
}

func TestThemeFuncsReachSlotsAndAreValidated(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Funcs = template.FuncMap{"shout": strings.ToUpper}
		th.Templates.Prompt.Label = `{{ shout .Label }}`
	})
	assert.NoError(t, err)
	b, err := c.templates.Input.Label.bind(c.funcs(profilePlain, roleStyles{}))
	assert.NoError(t, err)
	out, err := b.render(struct{ Label string }{"name"})
	assert.NoError(t, err)
	assert.Equal(t, "NAME", out.String())

	_, err = compileTemplates(t, func(th *Theme) {
		th.Funcs = template.FuncMap{"bold": strings.ToUpper, "ok": strings.ToUpper, "bad": 1}
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Funcs.bold: reserved")
	assert.Contains(t, err.Error(), "theme.Funcs.bad: want a function")
	assert.NotContains(t, err.Error(), "theme.Funcs.ok")
}

// truecolor supplies test helpers with truecolor output and an accent role.
func truecolor() template.FuncMap {
	return destination{
		profile: profileTrueColor,
		roles:   roleStyles{accent: text("\x1b[38;2;0;173;216m")},
		symbols: unicodeSymbols,
	}.funcs()
}

func TestStandaloneLegacyHelpersAreByteIdentical(t *testing.T) {
	funcs := truecolor()
	for src, want := range map[string]string{
		`{{ red "x" }}`:                 "\x1b[31mx\x1b[0m",
		`{{ bold "x" 1 }}`:              "\x1b[1mx1\x1b[0m",
		`{{ bgBlue "x" }}`:              "\x1b[44mx\x1b[0m",
		`{{ red (bold "x") }}`:          "\x1b[31m\x1b[1mx\x1b[0m\x1b[0m",
		`{{ red "" }}`:                  "\x1b[31m\x1b[0m",
		`{{ strike .S }}`:               "\x1b[9ma, b\x1b[0m",
		`{{ red (printf "%d" 5) }}`:     "\x1b[31m5\x1b[0m",
		`{{ dim "a\x1b[2Kb" }}`:         "\x1b[2ma\x1b[2Kb\x1b[0m", // cursor control is not a style
		`{{ red "\x1b[38;5;0mx" }}`:     "\x1b[31m\x1b[38;5;0mx\x1b[0m",
		`{{ red "\x1b[48;2;0;0;0mx" }}`: "\x1b[31m\x1b[48;2;0;0;0mx\x1b[0m",
	} {
		assert.Equal(t, want, execute(t, funcs, src, struct{ S []string }{[]string{"a", "b"}}))
	}
}

func TestNestedHelpersRestoreOuterStyle(t *testing.T) {
	funcs := truecolor()
	const accent = "\x1b[38;2;0;173;216m"
	for _, tc := range []struct{ name, src, want string }{
		{"legacy in legacy", `{{ red (bold "x") " y" }}`, "\x1b[31m\x1b[1mx\x1b[0m\x1b[31m y\x1b[0m"},
		{"fg in bg", `{{ bg "#000" (fg "#FFF" "a") "b" }}`,
			"\x1b[48;2;0;0;0m\x1b[38;2;255;255;255ma\x1b[39mb\x1b[49m"},
		{"legacy in fg", `{{ fg "#F00" (bgBlue "a") "b" }}`,
			"\x1b[38;2;255;0;0m\x1b[44ma\x1b[0m\x1b[38;2;255;0;0mb\x1b[39m"},
		{"fg in fg", `{{ fg "#F00" "a" (fg "#0F0" "b") "c" }}`,
			"\x1b[38;2;255;0;0ma\x1b[38;2;0;255;0mb\x1b[39m\x1b[38;2;255;0;0mc\x1b[39m"},
		{"legacy in semantic", `{{ accent (bold "a") "b" }}`, accent + "\x1b[1ma\x1b[0m" + accent + "b\x1b[39m"},
		{"semantic in legacy", `{{ bold (accent "a") "b" }}`, "\x1b[1m" + accent + "a\x1b[39mb\x1b[0m"},
		{"bold and dim share 22", `{{ dim "\x1b[1mx\x1b[22my" }}`, "\x1b[2m\x1b[1mx\x1b[22m\x1b[2my\x1b[0m"},
		{"selective reset of another channel", `{{ bold "\x1b[39mx" }}`, "\x1b[1m\x1b[39mx\x1b[0m"},
		{"three levels", `{{ red (bold (underline "a") "b") "c" }}`,
			"\x1b[31m\x1b[1m\x1b[4ma\x1b[0m\x1b[31m\x1b[1mb\x1b[0m\x1b[31mc\x1b[0m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, execute(t, funcs, tc.src, nil))
		})
	}
}

func TestHelpersFollowProfile(t *testing.T) {
	attrs := destination{profile: profileAttrs}.funcs()
	plain := destination{profile: profilePlain}.funcs()
	for _, tc := range []struct {
		funcs     template.FuncMap
		src, want string
	}{
		{attrs, `{{ red "x" }}`, "x"},
		{attrs, `{{ bold "x" }}`, "\x1b[1mx\x1b[0m"},
		{attrs, `{{ red (bold "x") "y" }}`, "\x1b[1mx\x1b[0my"},
		{attrs, `{{ fg "#F00" "x" }}`, "x"},
		{attrs, `{{ accent "x" }}`, "x"},
		{plain, `{{ bold (red "x") }}`, "x"},
		{plain, `{{ bg "#000" "x" }}`, "x"},
	} {
		assert.Equal(t, tc.want, execute(t, tc.funcs, tc.src, nil))
	}
	ansi16 := destination{profile: profileANSI16}.funcs()
	assert.Equal(t, "\x1b[91mx\x1b[39m", execute(t, ansi16, `{{ fg "#F00" "x" }}`, nil))
}

func TestColorHelpersComposeWithStringHelpers(t *testing.T) {
	up := template.FuncMap{"upper": func(s string) string { return "<" + s + ">" }}
	for _, tc := range []struct {
		profile   colorProfile
		src, want string
	}{
		{profileANSI16, `{{ fg "#F00" "hi" | upper }}`, "<\x1b[91mhi\x1b[39m>"},
		{profileANSI16, `{{ bg "#F00" "hi" | upper }}`, "<\x1b[101mhi\x1b[49m>"},
		{profilePlain, `{{ fg "#F00" "hi" | upper }}`, "<hi>"},
		{profilePlain, `{{ bg "#F00" "hi" | upper }}`, "<hi>"},
	} {
		funcs := destination{profile: tc.profile}.funcs()
		for k, v := range up {
			funcs[k] = v
		}
		assert.Equal(t, tc.want, execute(t, funcs, tc.src, nil))
	}
}

func TestColorHelperRejectsMalformedAtExecution(t *testing.T) {
	for _, src := range []string{`{{ fg "red" "x" }}`, `{{ bg "" "x" }}`, `{{ fg .C "x" }}`} {
		tmpl, err := template.New("t").Funcs(truecolor()).Parse(src)
		assert.NoError(t, err)
		err = tmpl.Execute(&strings.Builder{}, struct{ C string }{"#12"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "want \"#RRGGBB\"")
	}
}

func TestSymbolHelper(t *testing.T) {
	funcs := destination{symbols: ASCIISymbols()}.funcs()
	assert.Equal(t, ">", execute(t, funcs, `{{ symbol "Focus" }}`, nil))
	tmpl, err := template.New("t").Funcs(funcs).Parse(`{{ symbol "Nope" }}`)
	assert.NoError(t, err)
	assert.Error(t, tmpl.Execute(&strings.Builder{}, nil))
}

func TestClippingNestedHelpersKeepsStyleBalanced(t *testing.T) {
	out := text(execute(t, truecolor(), `{{ red (bold "ab") "cd" }}`, nil)).truncateColumns(3)
	assert.Equal(t, 3, out.width())
	assert.Equal(t, "\x1b[31m\x1b[1mab\x1b[0m\x1b[31m…\x1b[0m", out.String())
}

func TestConcurrentHelperExecutions(t *testing.T) {
	tmpl, err := template.New("t").Funcs(truecolor()).Parse(`{{ red (bold .) " y" }}`)
	assert.NoError(t, err)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			var b strings.Builder
			if err := tmpl.Execute(&b, i); err != nil {
				t.Error(err)
				return
			}
			want := fmt.Sprintf("\x1b[31m\x1b[1m%d\x1b[0m\x1b[31m y\x1b[0m", i)
			if b.String() != want {
				t.Errorf("got %q, want %q", b.String(), want)
			}
		})
	}
	wg.Wait()
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

func TestBoundSlotRendersOrFailsWhole(t *testing.T) {
	boom := errors.New("boom")
	c, err := compileTemplates(t, func(th *Theme) {
		th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
		th.Templates.Prompt.Label = `{{ bold .Label }}`
		th.Templates.Prompt.Hint = `{{ red "partial" }}{{ fails .Label }}`
		th.Templates.Prompt.Feedback = "{{ .Label }}\n"
		th.Templates.Prompt.Answer = "{{ .Label }}\x1b[2K"
		th.Templates.Choice.More = Hidden
	})
	assert.NoError(t, err)
	data := struct{ Label string }{"x"}
	render := func(s *compiledSlot, p colorProfile) (text, error) {
		b, err := s.bind(c.funcs(p, roleStyles{}))
		assert.NoError(t, err)
		return b.render(data)
	}

	out, err := render(c.templates.Prompt.Label, profileTrueColor)
	assert.NoError(t, err)
	assert.Equal(t, "\x1b[1mx\x1b[0m", out.String())
	out, err = render(c.templates.Prompt.Label, profilePlain)
	assert.NoError(t, err)
	assert.Equal(t, "x", out.String()) // binding does not change the installed slot

	out, err = render(c.templates.Prompt.Hint, profileTrueColor)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Templates.Prompt.Hint")
	assert.True(t, out == nil)

	for _, s := range []*compiledSlot{c.templates.Prompt.Feedback, c.templates.Prompt.Answer} {
		out, err = render(s, profileTrueColor)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "want text and SGR only")
		assert.True(t, out == nil)
	}

	out, err = render(c.templates.Choice.More, profileTrueColor)
	assert.NoError(t, err)
	assert.True(t, out == nil)
}

func TestThemeGeometryUsesInlineAndGuide(t *testing.T) {
	th := NewTheme()
	th.Symbols = ASCIISymbols()
	c, err := th.compile()
	assert.NoError(t, err)
	assert.Equal(t, "| ", c.geometry(10, 5).lead(true).String())
	assert.Equal(t, "", c.geometry(10, 5).lead(false).String())
	th.Inline = true
	c, err = th.compile()
	assert.NoError(t, err)
	assert.Equal(t, "", c.geometry(10, 5).lead(true).String())
}

func TestBuiltinPromptSlots(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Symbols = ASCIISymbols() })
	assert.NoError(t, err)
	render := func(s *compiledSlot, data any) string {
		t.Helper()
		b, err := s.bind(c.funcs(profilePlain, roleStyles{}))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates
	assert.Equal(t, "? Name", render(tt.Input.Label, labelView{"Name"}))
	assert.Equal(t, "? Name", render(tt.Password.Label, labelView{"Name"}))
	assert.Equal(t, "+ Name · v", render(tt.Input.Answer, answerView{Label: "Name", Value: "v"}))
	assert.Equal(t, "! bad", render(tt.Number.Feedback, feedbackView{"bad"}))
	keys := hintView{[]keyHint{{"up/down", "adjust"}, {"enter", "submit"}}}
	assert.Equal(t, "up/down adjust · enter submit", render(tt.Number.Hint, keys))
	assert.Equal(t, "", render(tt.Number.Hint, hintView{}))
	// built-ins stay out of the editable configuration
	assert.Equal(t, Template(""), c.editable().Templates.Prompt.Label)
}

func TestBuiltinMultichoiceSlots(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Symbols = ASCIISymbols() })
	assert.NoError(t, err)
	render := func(s *compiledSlot, data any) string {
		t.Helper()
		b, err := s.bind(c.funcs(profilePlain, roleStyles{}))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates.Multichoice
	for _, tc := range []struct {
		row  multichoiceRow
		want string
	}{
		{multichoiceRow{Value: "v", Active: true, Selected: true}, "> x v"},
		{multichoiceRow{Value: "v", Active: true}, "> o v"},
		{multichoiceRow{Value: "v", Selected: true}, "  x v"},
		{multichoiceRow{Value: "v"}, "  o v"},
	} {
		assert.Equal(t, tc.want, render(tt.Row, tc.row))
	}
	assert.Equal(t, "a, b", render(tt.AnswerValue, collectionView{Values: []string{"a", "b"}, Count: 2}))
	assert.Equal(t, "none", render(tt.AnswerValue, collectionView{}))
	assert.Equal(t, "No matches", render(tt.Empty, emptyView{"zz"}))
	assert.Equal(t, "+ Pick · a, b", render(tt.Answer, answerView{Label: "Pick", Value: "a, b"}))
}

func TestMultichoiceRowAndAnswerValueFallBackToBuiltins(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Multichoice.Label = "family"
		th.Templates.Multichoice.AnswerValue = Hidden
	})
	assert.NoError(t, err)
	builtin, err := builtinTemplates.compile(c.funcs(profilePlain, roleStyles{}), templates{})
	assert.NoError(t, err)
	tt := c.templates.Multichoice
	assert.Equal(t, builtin.Multichoice.Row.tmpl.Root.String(), tt.Row.tmpl.Root.String())
	assert.True(t, tt.AnswerValue.hidden)
	// a family edit does not reach the other families
	assert.Equal(t, "family", tt.Label.tmpl.Root.String())
	assert.True(t, c.templates.Dropdown.Label.tmpl.Root.String() != "family")
}

func TestColorLiteralsFailInstallation(t *testing.T) {
	for _, src := range []Template{
		`{{ fg "#2D9B5" .Label }}`,
		`{{ bg "teal" .Label }}`,
		`{{ fg "" .Label }}`,
		`{{ bold (bg "#FFF3C4" (fg "00ADD8" .Label)) }}`,
		`{{ if .Label }}{{ fg "#12345G" . }}{{ end }}`,
		`{{ range .Values }}{{ else }}{{ fg "#1234" "x" }}{{ end }}`,
		`{{ with .Label }}{{ . }}{{ else }}{{ bg "#FFFFFFFF" "x" }}{{ end }}`,
		`{{ define "x" }}{{ fg "rgb(0,0,0)" . }}{{ end }}{{ template "x" .Label }}`,
	} {
		_, err := compileTemplates(t, func(th *Theme) { th.Templates.Input.Label = src })
		if err == nil || !strings.Contains(err.Error(), "theme.Templates.Input.Label") ||
			!strings.Contains(err.Error(), `want "#RRGGBB" or "#RGB"`) {
			t.Errorf("%s: got %v", src, err)
		}
	}
	_, err := compileTemplates(t, func(th *Theme) { th.Templates.Input.Label = `{{ fg "#0AD" .Label }}` })
	assert.NoError(t, err)
	// dynamic colors, and text literals in the value position, are not checked
	_, err = compileTemplates(t, func(th *Theme) {
		th.Templates.Input.Label = `{{ fg .Color .Label }}{{ fg "#000" "not a color" }}{{ printf "%s" "teal" }}`
	})
	assert.NoError(t, err)
}

func TestColorLiteralErrorHasPosition(t *testing.T) {
	_, err := compileTemplates(t, func(th *Theme) { th.Templates.Prompt.Answer = "x\n{{ fg \"#12\" .Value }}" })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Prompt.Answer: Prompt.Answer:2:")
	assert.Contains(t, err.Error(), ": fg: ")
}

func TestThemeRolesKeepNeutralsDefaultOnAssumedBackground(t *testing.T) {
	c, err := NewTheme().compile()
	assert.NoError(t, err)
	assumed := c.bind(profileTrueColor, background{})
	assert.True(t, assumed.roles.foreground == nil && assumed.roles.muted == nil && assumed.roles.guide == nil)
	assert.True(t, assumed.roles.accent != nil && assumed.roles.success != nil && assumed.roles.failure != nil)

	measured := c.bind(profileTrueColor, background{rgb: sRGB{0x12, 0x12, 0x12}, measured: true})
	assert.True(t, measured.roles.foreground != nil && measured.roles.muted != nil && measured.roles.guide != nil)

	for _, p := range []colorProfile{profilePlain, profileAttrs} {
		assert.Equal(t, roleStyles{}, c.bind(p, background{rgb: sRGB{0x12, 0x12, 0x12}, measured: true}).roles)
	}
}

func TestThemeRolesHonorExplicitChoices(t *testing.T) {
	th := NewTheme()
	th.Light = true
	th.Palette.Dark.Muted = "#777777"
	th.Palette.Light.Accent = "#00ADD8" // too light for white, kept as given
	c, err := th.compile()
	assert.NoError(t, err)
	b := c.bind(profileTrueColor, background{})
	// a forced light variant trusts its reference background for neutrals
	assert.True(t, b.roles.foreground != nil)
	assert.Equal(t, "\x1b[38;2;0;173;216m", string(b.roles.accent))

	th.Light = false
	c, err = th.compile()
	assert.NoError(t, err)
	b = c.bind(profileTrueColor, background{})
	// an explicit neutral renders on an assumed background, automatic ones do not
	assert.Equal(t, "\x1b[38;2;119;119;119m", string(b.roles.muted))
	assert.True(t, b.roles.foreground == nil)
}

func TestThemeRolesHoldWordsToTextContrast(t *testing.T) {
	c, err := NewTheme().compile()
	assert.NoError(t, err)
	white := sRGB{0xFF, 0xFF, 0xFF}
	pal := c.palette.resolve(false, white, true)
	for _, p := range []colorProfile{profileANSI16, profileANSI256, profileTrueColor} {
		roles := c.roles(p, pal)
		for name, role := range map[string]text{"success": roles.success, "accent": roles.accent, "error": roles.failure} {
			if role == nil {
				continue // no readable shade: the terminal default
			}
			want, ok := p.readable(
				map[string]sRGB{"success": pal.Success, "accent": pal.Accent, "error": pal.Error}[name],
				white,
				contrastText,
			)
			assert.True(t, ok)
			assert.Equal(t, string(p.fg(want)), string(role))
		}
	}
}

func TestBindingStylesGuideAndValue(t *testing.T) {
	th := NewTheme()
	th.Symbols = ASCIISymbols()
	c, err := th.compile()
	assert.NoError(t, err)
	b := c.bind(profileTrueColor, background{rgb: sRGB{0x12, 0x12, 0x12}, measured: true})
	lead := b.geometry(10, 5).lead(true)
	assert.Equal(t, string(b.roles.guide)+"|\x1b[39m ", lead.String())
	assert.Equal(t, 2, lead.width())
	assert.Equal(t, "", b.style(b.roles.foreground, nil).String())
	assert.Equal(t, "| ", c.bind(profilePlain, background{}).geometry(10, 5).lead(true).String())
}

func TestBuiltinSpinnerSlots(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Symbols = ASCIISymbols() })
	assert.NoError(t, err)
	render := func(s *compiledSlot, data spinnerView) string {
		t.Helper()
		b, err := s.bind(c.funcs(profilePlain, roleStyles{}))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates.Spinner
	assert.Equal(t, ".. job: m", render(tt.Running, spinnerView{Frame: "..", Prefix: "job", Message: "m"}))
	assert.Equal(t, ".. m", render(tt.Running, spinnerView{Frame: "..", Message: "m"}))
	assert.Equal(t, "- job: m", render(tt.Stopped, spinnerView{Frame: "..", Prefix: "job", Message: "m"}))
	assert.Equal(t, "! m", render(tt.Failed, spinnerView{Frame: "..", Message: "m"}))
}

func TestSpinnerSlotsRejectHidden(t *testing.T) {
	_, err := compileTemplates(t, func(th *Theme) { th.Templates.Spinner.Stopped = Hidden })
	assert.Contains(t, err.Error(), "theme.Templates.Spinner.Stopped: empty source is not allowed")
}

func TestSpinnerStyleValidation(t *testing.T) {
	c, err := Theme{}.compile()
	assert.NoError(t, err)
	assert.Equal(t, []string{"▘", "▀", "▝", "▐", "▗", "▄", "▖", "▌"}, c.source.Symbols.Spinner)
	assert.Equal(t, 100*time.Millisecond, c.source.Spinner.Interval)
	c, err = Theme{Symbols: ASCIISymbols()}.compile()
	assert.NoError(t, err)
	assert.Equal(t, []string{"|", "/", "-", `\`}, c.source.Symbols.Spinner)

	th := NewTheme()
	th.Spinner = SpinnerStyle{Interval: -time.Second}
	_, err = th.compile()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Spinner.Interval: -1s: must not be negative")

	for _, tc := range []struct {
		frame string
		want  string
	}{
		{"", "theme.Symbols.Spinner[1]: must not be empty"},
		{"a\n", "theme.Symbols.Spinner[1]"},
		{"\x1b[32ma", "theme.Symbols.Spinner[1]"},
		{"ab", "theme.Symbols.Spinner[1]: \"ab\": want one display column"},
		{"\u754c", "theme.Symbols.Spinner[1]: \"\u754c\": want one display column, got 2"},
		{"\u0301", "theme.Symbols.Spinner[1]"},
	} {
		th := NewTheme()
		th.Symbols.Spinner = []string{"a", tc.frame}
		_, err := th.compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestSpinnerFramesAreCopied(t *testing.T) {
	keepTheme(t)
	th := NewTheme()
	th.Symbols.Spinner = []string{"a", "b"}
	assert.NoError(t, defaultIO.installTheme(th))
	installed := defaultIO.currentTheme()
	th.Symbols.Spinner[0] = "x"
	got := installed.editable()
	got.Symbols.Spinner[1] = "y"
	assert.Equal(t, []string{"a", "b"}, installed.source.Symbols.Spinner)
	// the presets are not shared with editable themes
	NewTheme().Symbols.Spinner[0] = "?"
	assert.Equal(t, "▘", NewTheme().Symbols.Spinner[0])
	assert.Equal(t, "▘", unicodeSymbols.Spinner[0])
}

func TestBuiltinProgressSlots(t *testing.T) {
	c, err := compileTemplates(t, func(*Theme) {})
	assert.NoError(t, err)
	render := func(s *compiledSlot, data progressView) string {
		t.Helper()
		b, err := s.bind(c.funcs(profilePlain, roleStyles{}))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates.Progress
	view := progressView{Label: "build", Percent: 7, Values: []string{"1.00/s", "3s remaining"}, Bar: "##"}
	assert.Equal(t, "build", render(tt.Label, view))
	assert.Equal(t, "1.00/s · 3s remaining", render(tt.Metrics, view))
	assert.Equal(t, "", render(tt.Metrics, progressView{}))
	view.Metrics = "m"
	assert.Equal(t, "build   7% ## m", render(tt.Frame, view))
	assert.Equal(t, "100% ##", render(tt.Frame, progressView{Percent: 100, Bar: "##"}))
}

func TestProgressFrameInsertsBarOnce(t *testing.T) {
	for _, frame := range []Template{
		`{{ .Bar }}`,
		`{{ .Label }} [{{- .Bar -}}] {{ with .Metrics }}{{ . }}{{ end }}`,
		`{{ define "m" }}{{ .Metrics }}{{ end }}{{ .Bar }}{{ template "m" . }}`,
	} {
		_, err := compileTemplates(t, func(th *Theme) { th.Templates.Progress.Frame = frame })
		assert.NoError(t, err)
	}
	for _, frame := range []Template{
		`{{ .Label }}`,
		`{{ .Bar }}{{ .Bar }}`,
		`{{ if .Label }}{{ .Bar }}{{ end }}`,
		`{{ with .Label }}{{ $.Bar }}{{ end }}`,
		`{{ printf "%s" .Bar }}`,
		`{{ .Bar | printf "%s" }}`,
		`{{ $b := .Bar }}{{ $b }}`,
		`{{ .Bar }}{{ len .Bar }}`,
		`{{ .Bar }}{{ if .Bar }}x{{ end }}`,
		`{{ .Bar }}{{ $.Bar }}`,
		`{{ .Bar }}{{ (.).Bar }}`,
		`{{ define "b" }}{{ .Bar }}{{ end }}{{ template "b" . }}`,
		`{{ define "b" }}{{ .Bar }}{{ end }}`,
	} {
		_, err := compileTemplates(t, func(th *Theme) { th.Templates.Progress.Frame = frame })
		assert.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			"theme.Templates.Progress.Frame: want one top-level {{ .Bar }} and no other use of .Bar",
		)
	}
	_, err := compileTemplates(t, func(th *Theme) { th.Templates.Progress.Frame = Hidden })
	assert.Contains(t, err.Error(), "theme.Templates.Progress.Frame: empty source is not allowed")
	// label and metrics may be hidden, and other slots stay unchecked
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Progress.Label = Hidden
		th.Templates.Progress.Metrics = `{{ .Bar }}{{ .Bar }}`
	})
	assert.NoError(t, err)
	assert.True(t, c.templates.Progress.Label.hidden)
}

func TestProgressStyleValidation(t *testing.T) {
	c, err := Theme{}.compile()
	assert.NoError(t, err)
	assert.Equal(t, progressStyle, c.source.Progress)
	th := NewTheme()
	th.Progress = ProgressStyle{MaxBarWidth: 3}
	th.Symbols.BarFill, th.Symbols.BarHead = "#", ""
	c, err = th.compile()
	assert.NoError(t, err)
	assert.Equal(t, ProgressStyle{MinBarWidth: 1, MaxBarWidth: 3}, c.source.Progress)
	assert.Equal(t, "#", c.source.Symbols.BarFill)
	assert.Equal(t, "█", c.source.Symbols.BarHead)
	for _, tc := range []struct {
		style ProgressStyle
		want  string
	}{
		{ProgressStyle{MinBarWidth: -1}, "theme.Progress.MinBarWidth: -1: must not be negative"},
		{ProgressStyle{MaxBarWidth: -1}, "theme.Progress.MaxBarWidth: -1: must not be negative"},
		{ProgressStyle{MinBarWidth: 5, MaxBarWidth: 4}, "theme.Progress.MaxBarWidth: 4: below MinBarWidth 5"},
	} {
		th := NewTheme()
		th.Progress = tc.style
		_, err := th.compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestBarSymbolsValidation(t *testing.T) {
	for _, tc := range []struct {
		edit func(*Symbols)
		want string
	}{
		{func(s *Symbols) { s.BarFill = "##" }, `theme.Symbols.BarFill: "##": want one display column, got 2`},
		{func(s *Symbols) { s.BarHead = "Ａ" }, `theme.Symbols.BarHead: "Ａ": want one display column, got 2`},
		{func(s *Symbols) { s.BarEmpty = "\t" }, `theme.Symbols.BarEmpty: "\t": control character`},
	} {
		th := NewTheme()
		tc.edit(&th.Symbols)
		_, err := th.compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
	for name, want := range map[string]string{"BarFill": "=", "BarHead": ">", "BarEmpty": "-"} {
		got, err := ASCIISymbols().named(name)
		assert.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

func TestBuiltinTableSlots(t *testing.T) {
	c, err := compileTemplates(t, func(*Theme) {})
	assert.NoError(t, err)
	render := func(p colorProfile, s *compiledSlot, data any) string {
		t.Helper()
		roles := roleStyles{foreground: text(cyan)}
		if p < profileANSI16 {
			roles = roleStyles{}
		}
		b, err := s.bind(c.funcs(p, roles))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates.Table
	column := columnView{Header: "NAME", Name: "Name", Kind: "string"}
	assert.Equal(t, "NAME", render(profilePlain, tt.Header, column))
	assert.Equal(t, bold+"NAME"+reset, render(profileAttrs, tt.Header, column))
	assert.Equal(t, bold+cyan+"NAME"+fgDefault+reset, render(profileTrueColor, tt.Header, column))
	assert.Equal(t, "x", render(profilePlain, tt.Cell, cellView{columnView: column, Value: "x"}))
	assert.Equal(t, cyan+"x"+fgDefault, render(profileTrueColor, tt.Cell, cellView{columnView: column, Value: "x"}))
}

func TestTableSlotsHiding(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Templates.Table.Header = Hidden })
	assert.NoError(t, err)
	assert.True(t, c.templates.Table.Header.hidden)
	_, err = compileTemplates(t, func(th *Theme) { th.Templates.Table.Cell = Hidden })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Table.Cell: empty source is not allowed")
	_, err = compileTemplates(t, func(th *Theme) { th.Templates.Table.Cell = `{{ fg "#12" .Value }}` })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Table.Cell: ")
}

func TestTableStyleValidation(t *testing.T) {
	c, err := Theme{}.compile()
	assert.NoError(t, err)
	assert.Equal(t, tableStyle, c.source.Table)
	th := NewTheme()
	th.Table = TableStyle{MinColumnWidth: 2}
	c, err = th.compile()
	assert.NoError(t, err)
	assert.Equal(t, TableStyle{ColumnGap: 2, MinColumnWidth: 2}, c.source.Table)
	for _, tc := range []struct {
		style TableStyle
		want  string
	}{
		{TableStyle{ColumnGap: -1}, "theme.Table.ColumnGap: -1: must not be negative"},
		{TableStyle{MinColumnWidth: -2}, "theme.Table.MinColumnWidth: -2: must not be negative"},
	} {
		th := NewTheme()
		th.Table = tc.style
		_, err := th.compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestDivergingScale(t *testing.T) {
	th := NewTheme()
	th.Light = true
	c, err := th.compile()
	assert.NoError(t, err)
	look := c.bind(profileTrueColor, background{light: true})
	scale := look.diverging()
	assert.Equal(t, scaleLevels, len(scale))
	assert.Equal(t, look.roles.success, scale[0])
	assert.Equal(t, look.roles.foreground, scale[2])
	assert.Equal(t, look.roles.failure, scale[4])
	seen := map[string]bool{}
	for _, level := range scale {
		assert.True(t, len(level) > 0)
		seen[string(level)] = true
	}
	assert.Equal(t, scaleLevels, len(seen))
	// the halfway levels are readable text
	for i, role := range []sRGB{look.palette.Success, look.palette.Error} {
		end, ok := c.role(profileTrueColor, look.palette, slot{}, role, contrastText, false)
		assert.True(t, ok)
		mid, ok := profileTrueColor.readable(end.oklab().mix(look.palette.Foreground.oklab(), 0.5),
			look.palette.Background, contrastText)
		assert.True(t, ok)
		assert.Equal(t, profileTrueColor.fg(mid), scale[1+2*i])
		assert.True(t, mid.contrast(look.palette.Background) >= contrastText)
	}

	// an override renders literally at the end and seeds the level next to it
	th.Palette.Light.Success = "#00FF00"
	c, err = th.compile()
	assert.NoError(t, err)
	over := c.bind(profileTrueColor, background{light: true}).diverging()
	assert.Equal(t, profileTrueColor.fg(RGB("#00FF00").must()), over[0])
	assert.True(t, string(over[1]) != string(scale[1]))
	assert.Equal(t, scale[3], over[3])

	for _, p := range []colorProfile{profilePlain, profileAttrs} {
		assert.Equal(t, make([]text, scaleLevels), c.bind(p, background{light: true}).diverging())
	}
	// an assumed background keeps the neutral middle at the terminal default
	dark, err := NewTheme().compile()
	assert.NoError(t, err)
	mid := dark.bind(profileTrueColor, background{}).diverging()[2]
	assert.Equal(t, 0, len(mid))
}

func TestSetDefaultThemePublishesAndKeepsPreviousOnError(t *testing.T) {
	keepTheme(t)
	WithFn("shout", strings.ToUpper)
	th := DefaultTheme()
	// unset colors stay unset, so they keep adapting
	assert.Equal(t, RGB(""), th.Palette.Dark.Foreground)
	_, ok := th.Funcs["shout"]
	assert.True(t, ok)

	th.Palette.Accent = "#0AD"
	th.Inline = true
	assert.NoError(t, SetDefaultTheme(th))
	th.Palette.Accent = "#FF0000" // later edits do not reach the installed copy
	installed := DefaultTheme()
	assert.Equal(t, RGB("#00AADD"), installed.Palette.Accent)
	assert.True(t, installed.Inline)

	bad := DefaultTheme()
	bad.Palette.Error = "red"
	err := SetDefaultTheme(bad)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error")
	assert.Equal(t, RGB("#00AADD"), DefaultTheme().Palette.Accent)
}
