// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"slices"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestResolveDefaultsBothAppearances(t *testing.T) {
	for _, light := range []bool{false, true} {
		r, err := Palette{}.resolve(light, SRGB{}, false)
		assert.NoError(t, err)
		assert.Equal(t, light, r.Light)
		assert.True(t, !r.Known)
		bg := r.Background
		assert.True(t, r.Foreground.Contrast(bg) >= ContrastText)
		assert.True(t, r.Muted.Contrast(bg) >= ContrastText)
		assert.True(t, r.Guide.Contrast(bg) >= ContrastGuide)
		assert.True(t, r.Accent.Contrast(bg) >= ContrastText)
		assert.True(t, r.Success.Contrast(bg) >= ContrastMark)
		assert.True(t, r.Error.Contrast(bg) >= ContrastText)
		assert.Equal(t, r.Guide, r.Track)
		// Muted is quieter than the foreground, guide quieter than muted.
		assert.True(t, r.Muted.Contrast(bg) <= r.Foreground.Contrast(bg))
		assert.True(t, r.Guide.Contrast(bg) <= r.Muted.Contrast(bg))
	}
}

func TestResolveMeasuredBackgroundPicksVariant(t *testing.T) {
	r, err := Palette{}.resolve(false, SRGB{250, 250, 250}, true)
	assert.NoError(t, err)
	assert.True(t, r.Light)
	assert.True(t, r.Known)
	assert.Equal(t, SRGB{250, 250, 250}, r.Background)
	assert.Equal(t, RGB("#232830").must(), r.Foreground)
	// A seed in the preset moves on a light terminal but not on a dark one.
	dark, err := Palette{}.resolve(true, SRGB{0, 0, 0}, true)
	assert.NoError(t, err)
	assert.Equal(t, RGB("#00ADD8").must(), dark.Accent)
	assert.True(t, r.Accent != dark.Accent)
}

func TestResolveDefaultNeutralsReadableAroundBoundary(t *testing.T) {
	for _, v := range []uint8{0x60, 0x70, 0x77, 0x80, 0x90, 0xA0} {
		bg := SRGB{v, v, v}
		r, err := Palette{}.resolve(false, bg, true)
		assert.NoError(t, err)
		assert.True(t, r.Foreground.Contrast(bg) >= ContrastText-0.2)
		assert.True(t, r.Muted.Contrast(bg) >= ContrastText-0.2)
		assert.True(t, r.Accent.Contrast(bg) >= ContrastText-0.2)
	}
}

func TestResolveExplicitForegroundIsLiteralOnAnyBackground(t *testing.T) {
	bg := SRGB{0x77, 0x77, 0x77}
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
	r, err := p.resolve(true, SRGB{}, false)
	assert.NoError(t, err)
	assert.Equal(t, RGB("#00ADD8").must(), r.Accent)
	assert.Equal(t, RGB("#CCCCCC").must(), r.Muted)
	assert.Equal(t, RGB("#00ADD8"), p.Accent)
	assert.Equal(t, RGB(""), p.Light.Foreground) // caller value never filled in
}

func TestResolveEquivalentSpellingsMatch(t *testing.T) {
	a, err := Palette{Accent: "#0AD"}.resolve(true, SRGB{}, false)
	assert.NoError(t, err)
	b, err := Palette{Accent: "#00aadd"}.resolve(true, SRGB{}, false)
	assert.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestResolveRejectsMalformedWithPath(t *testing.T) {
	p := Palette{}
	p.Dark.Accent = "00ADD"
	_, err := p.resolve(false, SRGB{}, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Palette.Dark.Accent")
	assert.Contains(t, err.Error(), `"00ADD"`)

	_, err = Palette{Error: "red"}.resolve(false, SRGB{}, false)
	assert.Contains(t, err.Error(), "theme.Palette.Error")
}

func TestParsedPaletteResolveMatchesAdapter(t *testing.T) {
	p := Palette{Accent: "#00ADD8", Dark: PaletteVariant{Muted: "#777777"}}
	want, err := p.resolve(false, SRGB{0x20, 0x20, 0x20}, true)
	assert.NoError(t, err)
	pp, err := p.parse()
	assert.NoError(t, err)
	assert.Equal(t, want, pp.resolve(false, SRGB{0x20, 0x20, 0x20}, true))
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
	assumed := c.bind(ProfileTrueColor, Background{})
	assert.True(t, assumed.roles.foreground == nil && assumed.roles.muted == nil)
	// the guide turns faint, so that it stays quieter than the muted role
	assert.Equal(t, Text(Dim), assumed.roles.guide)
	assert.True(t, assumed.roles.accent != nil && assumed.roles.success != nil && assumed.roles.failure != nil)

	measured := c.bind(ProfileTrueColor, Background{RGB: SRGB{0x12, 0x12, 0x12}, Measured: true})
	assert.True(t, measured.roles.foreground != nil && measured.roles.muted != nil && measured.roles.guide != nil)

	for _, p := range []Profile{ProfilePlain, ProfileAttrs} {
		assert.Equal(t, roleStyles{}, c.bind(p, Background{RGB: SRGB{0x12, 0x12, 0x12}, Measured: true}).roles)
	}
}

func TestThemeRolesHonorExplicitChoices(t *testing.T) {
	th := NewTheme()
	th.Light = true
	th.Palette.Dark.Muted = "#777777"
	th.Palette.Light.Accent = "#00ADD8" // too light for white, kept as given
	c, err := th.compile()
	assert.NoError(t, err)
	b := c.bind(ProfileTrueColor, Background{})
	// a forced light variant trusts its reference background for neutrals
	assert.True(t, b.roles.foreground != nil)
	assert.Equal(t, "\x1b[38;2;0;173;216m", string(b.roles.accent))

	th.Light = false
	c, err = th.compile()
	assert.NoError(t, err)
	b = c.bind(ProfileTrueColor, Background{})
	// an explicit neutral renders on an assumed background, automatic ones do not
	assert.Equal(t, "\x1b[38;2;119;119;119m", string(b.roles.muted))
	assert.True(t, b.roles.foreground == nil)
}

func TestThemeRolesHoldWordsToTextContrast(t *testing.T) {
	c, err := NewTheme().compile()
	assert.NoError(t, err)
	white := SRGB{0xFF, 0xFF, 0xFF}
	pal := c.palette.resolve(false, white, true)
	for _, p := range []Profile{ProfileANSI16, ProfileANSI256, ProfileTrueColor} {
		roles := c.roles(p, pal)
		for name, role := range map[string]Text{"success": roles.success, "accent": roles.accent, "error": roles.failure} {
			if role == nil {
				continue // no readable shade: the terminal default
			}
			want, ok := p.Readable(
				map[string]SRGB{"success": pal.Success, "accent": pal.Accent, "error": pal.Error}[name],
				white,
				ContrastText,
			)
			assert.True(t, ok)
			assert.Equal(t, string(p.Fg(want)), string(role))
		}
	}
}

func TestBindingStylesGuideAndValue(t *testing.T) {
	th := NewTheme()
	th.Symbols = asciiSymbols
	c, err := th.compile()
	assert.NoError(t, err)
	b := c.bind(ProfileTrueColor, Background{RGB: SRGB{0x12, 0x12, 0x12}, Measured: true})
	lead := b.geometry(10, 5).lead(true)
	assert.Equal(t, string(b.roles.guide)+"|\x1b[39m ", lead.String())
	assert.Equal(t, 2, lead.Width())
	assert.Equal(t, "", b.style(b.roles.foreground, nil).String())
	assert.Equal(t, "| ", c.bind(ProfilePlain, Background{}).geometry(10, 5).lead(true).String())
}

func TestDivergingScale(t *testing.T) {
	th := NewTheme()
	th.Light = true
	c, err := th.compile()
	assert.NoError(t, err)
	look := c.bind(ProfileTrueColor, Background{Light: true})
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
	for i, role := range []SRGB{look.palette.Success, look.palette.Error} {
		end, ok := c.role(ProfileTrueColor, look.palette, slot{}, role, ContrastText, false)
		assert.True(t, ok)
		mid, ok := ProfileTrueColor.Readable(end.OKLab().Mix(look.palette.Foreground.OKLab(), 0.5),
			look.palette.Background, ContrastText)
		assert.True(t, ok)
		assert.Equal(t, ProfileTrueColor.Fg(mid), scale[1+2*i])
		assert.True(t, mid.Contrast(look.palette.Background) >= ContrastText)
	}

	// an override renders literally at the end and seeds the level next to it
	th.Palette.Light.Success = "#00FF00"
	c, err = th.compile()
	assert.NoError(t, err)
	over := c.bind(ProfileTrueColor, Background{Light: true}).diverging()
	assert.Equal(t, ProfileTrueColor.Fg(RGB("#00FF00").must()), over[0])
	assert.True(t, string(over[1]) != string(scale[1]))
	assert.Equal(t, scale[3], over[3])

	for _, p := range []Profile{ProfilePlain, ProfileAttrs} {
		assert.Equal(t, make([]Text, scaleLevels), c.bind(p, Background{Light: true}).diverging())
	}
	// an assumed background keeps the neutral middle at the terminal default
	dark, err := NewTheme().compile()
	assert.NoError(t, err)
	mid := dark.bind(ProfileTrueColor, Background{}).diverging()[2]
	assert.Equal(t, 0, len(mid))
}

func TestSixteenColorRolesKeepTheirMeaning(t *testing.T) {
	c, err := NewTheme().compile()
	assert.NoError(t, err)
	for _, bg := range []SRGB{{0, 0, 0}, {30, 30, 30}, {255, 255, 255}} {
		r := c.bind(ProfileANSI16, Background{RGB: bg, Light: bg.IsLight(), Measured: true}).roles
		for _, gray := range []Text{r.foreground, r.muted, r.guide} {
			assert.True(
				t,
				slices.Contains([]string{"\x1b[30m", "\x1b[37m", "\x1b[90m", "\x1b[97m", "\x1b[2m"}, string(gray)),
			)
		}
		assert.True(t, slices.Contains([]string{"\x1b[36m", "\x1b[96m"}, string(r.accent)))
	}
}
