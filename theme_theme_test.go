// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

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
	_, err = asciiSymbols.normalize()
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

func TestThemeRoundTripKeepsTemplateSources(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Templates.Choice.Empty = Hidden })
	if err != nil {
		t.Fatal(err)
	}
	if got := c.editable().Templates.Choice.Empty; got != Hidden {
		t.Fatalf("explicit empty lost in round trip: %+v", got)
	}
	if c.editable().Templates.Choice.Inactive != "" {
		t.Fatal("inherited slot became explicit")
	}
}

func TestRGBParse(t *testing.T) {
	for _, tc := range []struct {
		in   RGB
		want SRGB
	}{
		{"#00ADD8", SRGB{0x00, 0xAD, 0xD8}},
		{"#00add8", SRGB{0x00, 0xAD, 0xD8}},
		{"#0AD", SRGB{0x00, 0xAA, 0xDD}},
		{"#0ad", SRGB{0x00, 0xAA, 0xDD}},
		{"#000000", SRGB{}},
		{"#FFF", SRGB{255, 255, 255}},
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
	assert.Equal(t, SRGB{}, got)
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
