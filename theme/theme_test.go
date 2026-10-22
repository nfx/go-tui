// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"github.com/nfx/go-tui/internal/color"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestSymbolPresetsAreOneColumn(t *testing.T) {
	_, err := Symbols{}.normalize()
	assert.NoError(t, err)
	_, err = asciiSymbols.normalize()
	assert.NoError(t, err)
}

func TestRGBParse(t *testing.T) {
	for _, tc := range []struct {
		in   RGB
		want color.SRGB
	}{
		{"#00ADD8", color.SRGB{0x00, 0xAD, 0xD8}},
		{"#00add8", color.SRGB{0x00, 0xAD, 0xD8}},
		{"#0AD", color.SRGB{0x00, 0xAA, 0xDD}},
		{"#0ad", color.SRGB{0x00, 0xAA, 0xDD}},
		{"#000000", color.SRGB{}},
		{"#FFF", color.SRGB{255, 255, 255}},
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
	assert.Equal(t, color.SRGB{}, got)
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

func TestSymbolsNamedAndArrows(t *testing.T) {
	s := New().Symbols.ASCII()
	got, err := s.Named("Up")
	assert.NoError(t, err)
	assert.Equal(t, s.Up, got)
	_, err = s.Named("Nope")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `symbol "Nope": no such symbol`)
	assert.Equal(t, s.Up+s.Down, s.Arrows().String())
}
