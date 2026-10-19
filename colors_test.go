// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"strconv"
	"strings"
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
