// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestAnsciiFormatterHandlesTypes(t *testing.T) {
	str := "hi"
	ptr := &str
	arr := []string{"alpha", "beta"}
	num := 5
	formatter := ansciiFormatter("\x1b[31m", "\x1b[1m")

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

func TestWithFnRegistersFunc(t *testing.T) {
	name := "testFunc"
	orig, had := colorFns[name]
	WithFn(name, func(...any) string { return "ok" })
	t.Cleanup(func() {
		if had {
			colorFns[name] = orig
		} else {
			delete(colorFns, name)
		}
	})

	raw, ok := colorFns[name]
	if !ok {
		t.Fatalf("registration failed")
	}
	fn, ok := raw.(func(...any) string)
	if !ok {
		t.Fatalf("unexpected function type")
	}
	if fn() != "ok" {
		t.Fatalf("unexpected return value")
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
	for name, th := range map[string]Theme{"guided": GuidedTheme()} {
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
	th := GuidedTheme()
	assert.Equal(t, RGB(""), th.Palette.Light.Foreground)
	assert.Equal(t, RGB(""), th.Palette.Dark.Foreground)
	c, err := th.compile()
	assert.NoError(t, err)
	assert.True(t, !c.palette.dark.foreground.set)
	assert.True(t, !c.palette.light.muted.set)
}

func TestRoundTripKeepsIntentAndCanonicalizes(t *testing.T) {
	th := GuidedTheme()
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
	th := GuidedTheme()
	th.Palette.Dark.Accent = "00ADD8"
	_, err := th.compile()
	assert.Contains(t, err.Error(), "theme.Palette.Dark.Accent")
}

func TestInstallAtomicAndIsolation(t *testing.T) {
	prev := defaultIO.currentTheme()
	t.Cleanup(func() { defaultIO.theme.Store(prev) })

	th := GuidedTheme()
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
	bad := GuidedTheme()
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
			th := GuidedTheme()
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

// compileTemplates compiles a Guided theme with test-specific edits.
func compileTemplates(t *testing.T, edit func(*Theme)) (*compiledTheme, error) {
	t.Helper()
	th := GuidedTheme()
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
	if tt.Multichoice.Hint != nil {
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
