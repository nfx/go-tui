// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

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

func TestParsedPaletteResolveMatchesAdapter(t *testing.T) {
	p := Palette{Accent: "#00ADD8", Dark: PaletteVariant{Muted: "#777777"}}
	want, err := p.resolve(false, sRGB{0x20, 0x20, 0x20}, true)
	assert.NoError(t, err)
	pp, err := p.parse()
	assert.NoError(t, err)
	assert.Equal(t, want, pp.resolve(false, sRGB{0x20, 0x20, 0x20}, true))
}

func TestThemeGeometryUsesInlineAndGuide(t *testing.T) {
	th := NewTheme()
	th.Symbols = asciiSymbols
	c, err := th.compile()
	assert.NoError(t, err)
	assert.Equal(t, "| ", c.geometry(10, 5).lead(true).String())
	assert.Equal(t, "", c.geometry(10, 5).lead(false).String())
	th.Inline = true
	c, err = th.compile()
	assert.NoError(t, err)
	assert.Equal(t, "", c.geometry(10, 5).lead(true).String())
}

func TestThemeRolesKeepNeutralsDefaultOnAssumedBackground(t *testing.T) {
	c, err := NewTheme().compile()
	assert.NoError(t, err)
	assumed := c.bind(profileTrueColor, background{})
	assert.True(t, assumed.roles.foreground == nil && assumed.roles.muted == nil)
	// the guide turns faint, so that it stays quieter than the muted role
	assert.Equal(t, text(dim), assumed.roles.guide)
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
	th.Symbols = asciiSymbols
	c, err := th.compile()
	assert.NoError(t, err)
	b := c.bind(profileTrueColor, background{rgb: sRGB{0x12, 0x12, 0x12}, measured: true})
	lead := b.geometry(10, 5).lead(true)
	assert.Equal(t, string(b.roles.guide)+"|\x1b[39m ", lead.String())
	assert.Equal(t, 2, lead.width())
	assert.Equal(t, "", b.style(b.roles.foreground, nil).String())
	assert.Equal(t, "| ", c.bind(profilePlain, background{}).geometry(10, 5).lead(true).String())
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
