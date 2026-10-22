// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/theme"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestJSONStyleDerivesFromResolvedRoles(t *testing.T) {
	look := func(p theme.Palette) jsonLook {
		th := theme.New()
		th.Palette = p
		return newJSONLook(jsonBound(t, th, color.ProfileTrueColor, measuredBG(color.SRGB{0x12, 0x12, 0x12})))
	}
	base := look(theme.Palette{})
	accent := look(theme.Palette{Accent: "#2D9B55"})
	assert.Equal(t, 5, len(base.keys))
	assert.True(t, !bytes.Equal(base.keys[0], accent.keys[0]))
	assert.Equal(t, string(base.value), string(accent.value))
	assert.Equal(t, string(base.punct), string(accent.punct))

	fg := look(theme.Palette{Dark: theme.PaletteVariant{Foreground: "#FFE0A0"}})
	assert.True(t, !bytes.Equal(base.value, fg.value))
	assert.Equal(t, string(base.keys[0]), string(fg.keys[0]))
	assert.True(t, !bytes.Equal(base.punct, fg.punct)) // muted derives from foreground

	muted := look(theme.Palette{Dark: theme.PaletteVariant{Muted: "#888888"}})
	assert.True(t, !bytes.Equal(base.punct, muted.punct))
	assert.Equal(t, string(base.value), string(muted.value))
}

func TestJSONKeyShadesDimMonotonicallyOnEveryProfile(t *testing.T) {
	pal := legacyJSONPalette()
	for _, p := range []color.Profile{color.ProfileANSI16, color.ProfileANSI256, color.ProfileTrueColor} {
		keys := newJSONLook(presetJSONTheme().Bind(p, color.Background{})).keys
		assert.True(t, len(keys) > 0)
		prev := 22.0
		for _, k := range keys {
			c := displayed(t, p, k).Contrast(pal.Background)
			assert.True(t, c < prev)                // strictly dimmer, so also distinct
			assert.True(t, c >= color.ContrastText) // keys are words
			prev = c
		}
	}
	// the truecolor ramp has every level
	assert.Equal(t, jsonLevels, len(testJSONLook().keys))
}

func TestJSONKeysAcrossLightAndDarkBackgrounds(t *testing.T) {
	for _, bg := range []color.SRGB{{0, 0, 0}, {0x12, 0x12, 0x12}, {0x77, 0x77, 0x77}, {0xEE, 0xEE, 0xEE}, {255, 255, 255}} {
		for _, p := range []color.Profile{color.ProfileANSI16, color.ProfileANSI256, color.ProfileTrueColor} {
			prev := 22.0
			for _, k := range newJSONLook(jsonBound(t, theme.New(), p, measuredBG(bg))).keys {
				c := displayed(t, p, k).Contrast(bg)
				assert.True(t, c < prev)
				assert.True(t, c >= color.ContrastText)
				prev = c
			}
		}
	}
}

func TestJSONStyleWithoutColor(t *testing.T) {
	st := newJSONLook(presetJSONTheme().Bind(color.ProfileAttrs, color.Background{}))
	assert.Equal(t, ansi.Text(ansi.Bold), st.punct)
	assert.Equal(t, 0, len(st.keys))
	assert.Equal(t, 0, len(st.value))
}

func TestJSONStyleWithKnownBackgroundColorsNeutrals(t *testing.T) {
	look := jsonBound(t, theme.New(), color.ProfileTrueColor, measuredBG(color.SRGB{0xFF, 0xFF, 0xFF}))
	st := newJSONLook(look)
	assert.True(t, len(look.Roles.Foreground) > 0)
	assert.Equal(t, string(look.Roles.Foreground), string(st.value))
	assert.Equal(t, string(look.Roles.Muted), string(st.punct))
}
