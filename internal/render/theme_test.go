// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/theme"
	"slices"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestResolveDefaultsBothAppearances(t *testing.T) {
	for _, light := range []bool{false, true} {
		r, err := resolvePalette(theme.Palette{}, light, color.SRGB{}, false)
		assert.NoError(t, err)
		assert.Equal(t, light, r.Light)
		assert.True(t, !r.Known)
		bg := r.Background
		assert.True(t, r.Foreground.Contrast(bg) >= color.ContrastText)
		assert.True(t, r.Muted.Contrast(bg) >= color.ContrastText)
		assert.True(t, r.Guide.Contrast(bg) >= color.ContrastGuide)
		assert.True(t, r.Accent.Contrast(bg) >= color.ContrastText)
		assert.True(t, r.Success.Contrast(bg) >= color.ContrastMark)
		assert.True(t, r.Error.Contrast(bg) >= color.ContrastText)
		assert.Equal(t, r.Guide, r.Track)
		// Muted is quieter than the foreground, guide quieter than muted.
		assert.True(t, r.Muted.Contrast(bg) <= r.Foreground.Contrast(bg))
		assert.True(t, r.Guide.Contrast(bg) <= r.Muted.Contrast(bg))
	}
}

func TestResolveMeasuredBackgroundPicksVariant(t *testing.T) {
	r, err := resolvePalette(theme.Palette{}, false, color.SRGB{250, 250, 250}, true)
	assert.NoError(t, err)
	assert.True(t, r.Light)
	assert.True(t, r.Known)
	assert.Equal(t, color.SRGB{250, 250, 250}, r.Background)
	assert.Equal(t, color.MustHex("#232830"), r.Foreground)
	// A seed in the preset moves on a light terminal but not on a dark one.
	dark, err := resolvePalette(theme.Palette{}, true, color.SRGB{0, 0, 0}, true)
	assert.NoError(t, err)
	assert.Equal(t, color.MustHex("#00ADD8"), dark.Accent)
	assert.True(t, r.Accent != dark.Accent)
}

func TestResolveDefaultNeutralsReadableAroundBoundary(t *testing.T) {
	for _, v := range []uint8{0x60, 0x70, 0x77, 0x80, 0x90, 0xA0} {
		bg := color.SRGB{v, v, v}
		r, err := resolvePalette(theme.Palette{}, false, bg, true)
		assert.NoError(t, err)
		assert.True(t, r.Foreground.Contrast(bg) >= color.ContrastText-0.2)
		assert.True(t, r.Muted.Contrast(bg) >= color.ContrastText-0.2)
		assert.True(t, r.Accent.Contrast(bg) >= color.ContrastText-0.2)
	}
}

func TestResolveExplicitForegroundIsLiteralOnAnyBackground(t *testing.T) {
	bg := color.SRGB{0x77, 0x77, 0x77}
	r, err := resolvePalette(theme.Palette{
		Light: theme.PaletteVariant{Foreground: "#232830"},
		Dark:  theme.PaletteVariant{Foreground: "#232830"},
	},
		false,
		bg,
		true,
	)
	assert.NoError(t, err)
	assert.Equal(t, color.MustHex("#232830"), r.Foreground)
}

func TestResolveOverrideIsLiteralAndSeedUnchanged(t *testing.T) {
	p := theme.Palette{Accent: "#00ADD8"}
	p.Light.Accent = "#00ADD8" // deliberately low contrast on white
	p.Light.Muted = "#CCCCCC"
	r, err := resolvePalette(p, true, color.SRGB{}, false)
	assert.NoError(t, err)
	assert.Equal(t, color.MustHex("#00ADD8"), r.Accent)
	assert.Equal(t, color.MustHex("#CCCCCC"), r.Muted)
	assert.Equal(t, theme.RGB("#00ADD8"), p.Accent)
	assert.Equal(t, theme.RGB(""), p.Light.Foreground) // caller value never filled in
}

func TestResolveEquivalentSpellingsMatch(t *testing.T) {
	a, err := resolvePalette(theme.Palette{Accent: "#0AD"}, true, color.SRGB{}, false)
	assert.NoError(t, err)
	b, err := resolvePalette(theme.Palette{Accent: "#00aadd"}, true, color.SRGB{}, false)
	assert.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestResolveRejectsMalformedWithPath(t *testing.T) {
	p := theme.Palette{}
	p.Dark.Accent = "00ADD"
	_, err := resolvePalette(p, false, color.SRGB{}, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Palette.Dark.Accent")
	assert.Contains(t, err.Error(), `"00ADD"`)

	_, err = resolvePalette(theme.Palette{Error: "red"}, false, color.SRGB{}, false)
	assert.Contains(t, err.Error(), "theme.Palette.Error")
}

func TestParsedPaletteResolveMatchesAdapter(t *testing.T) {
	p := theme.Palette{Accent: "#00ADD8", Dark: theme.PaletteVariant{Muted: "#777777"}}
	want, err := resolvePalette(p, false, color.SRGB{0x20, 0x20, 0x20}, true)
	assert.NoError(t, err)
	pp, err := parsePalette(p)
	assert.NoError(t, err)
	assert.Equal(t, want, pp.Resolve(false, color.SRGB{0x20, 0x20, 0x20}, true))
}

func TestThemeGeometryUsesInlineAndGuide(t *testing.T) {
	th := theme.New()
	th.Symbols = theme.New().Symbols.ASCII()
	c, err := Compile(th)
	assert.NoError(t, err)
	assert.Equal(t, "| ", c.Geometry(10, 5).lead(true).String())
	assert.Equal(t, "", c.Geometry(10, 5).lead(false).String())
	th.Inline = true
	c, err = Compile(th)
	assert.NoError(t, err)
	assert.Equal(t, "", c.Geometry(10, 5).lead(true).String())
}

func TestThemeRolesKeepNeutralsDefaultOnAssumedBackground(t *testing.T) {
	c, err := Compile(theme.New())
	assert.NoError(t, err)
	assumed := c.Bind(color.ProfileTrueColor, color.Background{})
	assert.True(t, assumed.Roles.Foreground == nil && assumed.Roles.Muted == nil)
	// the guide turns faint, so that it stays quieter than the muted role
	assert.Equal(t, ansi.Text(ansi.Dim), assumed.Roles.Guide)
	assert.True(t, assumed.Roles.Accent != nil && assumed.Roles.Success != nil && assumed.Roles.Failure != nil)

	measured := c.Bind(color.ProfileTrueColor, color.Background{RGB: color.SRGB{0x12, 0x12, 0x12}, Measured: true})
	assert.True(t, measured.Roles.Foreground != nil && measured.Roles.Muted != nil && measured.Roles.Guide != nil)

	for _, p := range []color.Profile{color.ProfilePlain, color.ProfileAttrs} {
		assert.Equal(
			t,
			Roles{},
			c.Bind(p, color.Background{RGB: color.SRGB{0x12, 0x12, 0x12}, Measured: true}).Roles,
		)
	}
}

func TestThemeRolesHonorExplicitChoices(t *testing.T) {
	th := theme.New()
	th.Light = true
	th.Palette.Dark.Muted = "#777777"
	th.Palette.Light.Accent = "#00ADD8" // too light for white, kept as given
	c, err := Compile(th)
	assert.NoError(t, err)
	b := c.Bind(color.ProfileTrueColor, color.Background{})
	// a forced light variant trusts its reference background for neutrals
	assert.True(t, b.Roles.Foreground != nil)
	assert.Equal(t, "\x1b[38;2;0;173;216m", string(b.Roles.Accent))

	th.Light = false
	c, err = Compile(th)
	assert.NoError(t, err)
	b = c.Bind(color.ProfileTrueColor, color.Background{})
	// an explicit neutral renders on an assumed background, automatic ones do not
	assert.Equal(t, "\x1b[38;2;119;119;119m", string(b.Roles.Muted))
	assert.True(t, b.Roles.Foreground == nil)
}

func TestThemeRolesHoldWordsToTextContrast(t *testing.T) {
	c, err := Compile(theme.New())
	assert.NoError(t, err)
	white := color.SRGB{0xFF, 0xFF, 0xFF}
	pal := c.Palette.Resolve(false, white, true)
	for _, p := range []color.Profile{color.ProfileANSI16, color.ProfileANSI256, color.ProfileTrueColor} {
		roles := c.Roles(p, pal)
		for name, role := range map[string]ansi.Text{"success": roles.Success, "accent": roles.Accent, "error": roles.Failure} {
			if role == nil {
				continue // no readable shade: the terminal default
			}
			want, ok := p.Readable(
				map[string]color.SRGB{"success": pal.Success, "accent": pal.Accent, "error": pal.Error}[name],
				white,
				color.ContrastText,
			)
			assert.True(t, ok)
			assert.Equal(t, string(p.Fg(want)), string(role))
		}
	}
}

func TestBindingStylesGuideAndValue(t *testing.T) {
	th := theme.New()
	th.Symbols = theme.New().Symbols.ASCII()
	c, err := Compile(th)
	assert.NoError(t, err)
	b := c.Bind(color.ProfileTrueColor, color.Background{RGB: color.SRGB{0x12, 0x12, 0x12}, Measured: true})
	lead := b.Geometry(10, 5).lead(true)
	assert.Equal(t, string(b.Roles.Guide)+"|\x1b[39m ", lead.String())
	assert.Equal(t, 2, lead.Width())
	assert.Equal(t, "", b.Style(b.Roles.Foreground, nil).String())
	assert.Equal(t, "| ", c.Bind(color.ProfilePlain, color.Background{}).Geometry(10, 5).lead(true).String())
}

func TestDivergingScale(t *testing.T) {
	th := theme.New()
	th.Light = true
	c, err := Compile(th)
	assert.NoError(t, err)
	look := c.Bind(color.ProfileTrueColor, color.Background{Light: true})
	scale := look.Diverging()
	assert.Equal(t, scaleLevels, len(scale))
	assert.Equal(t, look.Roles.Success, scale[0])
	assert.Equal(t, look.Roles.Foreground, scale[2])
	assert.Equal(t, look.Roles.Failure, scale[4])
	seen := map[string]bool{}
	for _, level := range scale {
		assert.True(t, len(level) > 0)
		seen[string(level)] = true
	}
	assert.Equal(t, scaleLevels, len(seen))
	// the halfway levels are readable text
	for i, role := range []color.SRGB{look.Palette.Success, look.Palette.Error} {
		end, ok := c.Role(color.ProfileTrueColor, look.Palette, slot{}, role, color.ContrastText, false)
		assert.True(t, ok)
		mid, ok := color.ProfileTrueColor.Readable(end.OKLab().Mix(look.Palette.Foreground.OKLab(), 0.5),
			look.Palette.Background, color.ContrastText)
		assert.True(t, ok)
		assert.Equal(t, color.ProfileTrueColor.Fg(mid), scale[1+2*i])
		assert.True(t, mid.Contrast(look.Palette.Background) >= color.ContrastText)
	}

	// an override renders literally at the end and seeds the level next to it
	th.Palette.Light.Success = "#00FF00"
	c, err = Compile(th)
	assert.NoError(t, err)
	over := c.Bind(color.ProfileTrueColor, color.Background{Light: true}).Diverging()
	assert.Equal(t, color.ProfileTrueColor.Fg(color.MustHex("#00FF00")), over[0])
	assert.True(t, string(over[1]) != string(scale[1]))
	assert.Equal(t, scale[3], over[3])

	for _, p := range []color.Profile{color.ProfilePlain, color.ProfileAttrs} {
		assert.Equal(t, make([]ansi.Text, scaleLevels), c.Bind(p, color.Background{Light: true}).Diverging())
	}
	// an assumed background keeps the neutral middle at the terminal default
	dark, err := Compile(theme.New())
	assert.NoError(t, err)
	mid := dark.Bind(color.ProfileTrueColor, color.Background{}).Diverging()[2]
	assert.Equal(t, 0, len(mid))
}

func TestSixteenColorRolesKeepTheirMeaning(t *testing.T) {
	c, err := Compile(theme.New())
	assert.NoError(t, err)
	for _, bg := range []color.SRGB{{0, 0, 0}, {30, 30, 30}, {255, 255, 255}} {
		r := c.Bind(color.ProfileANSI16, color.Background{RGB: bg, Light: bg.IsLight(), Measured: true}).Roles
		for _, gray := range []ansi.Text{r.Foreground, r.Muted, r.Guide} {
			assert.True(
				t,
				slices.Contains([]string{"\x1b[30m", "\x1b[37m", "\x1b[90m", "\x1b[97m", "\x1b[2m"}, string(gray)),
			)
		}
		assert.True(t, slices.Contains([]string{"\x1b[36m", "\x1b[96m"}, string(r.Accent)))
	}
}

func TestPresetsCompile(t *testing.T) {
	for name, th := range map[string]theme.Theme{"builtin": theme.New()} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(th)
			assert.NoError(t, err)
		})
	}
}

func TestZeroThemeCompiles(t *testing.T) {
	_, err := Compile(theme.Theme{})
	assert.NoError(t, err)
}

func TestPresetKeepsAdaptiveNeutralsUnset(t *testing.T) {
	th := theme.New()
	assert.Equal(t, theme.RGB(""), th.Palette.Light.Foreground)
	assert.Equal(t, theme.RGB(""), th.Palette.Dark.Foreground)
	c, err := Compile(th)
	assert.NoError(t, err)
	assert.True(t, !c.Palette.dark.foreground.set)
	assert.True(t, !c.Palette.light.Variant.set)
}

func TestRoundTripKeepsIntentAndCanonicalizes(t *testing.T) {
	th := theme.New()
	th.Palette.Accent = "#0ad"
	th.Palette.Dark.Error = "#000"
	c, err := Compile(th)
	assert.NoError(t, err)
	got := c.Editable()
	assert.Equal(t, theme.RGB("#00AADD"), got.Palette.Accent)
	assert.Equal(t, theme.RGB("#000000"), got.Palette.Dark.Error) // explicit black stays explicit
	assert.Equal(t, theme.RGB(""), got.Palette.Dark.Foreground)
	again, err := Compile(got)
	assert.NoError(t, err)
	assert.True(t, again.Palette.dark.err.set)
	assert.True(t, !again.Palette.dark.foreground.set)
	assert.Equal(t, theme.RGB("#0ad"), th.Palette.Accent) // caller value untouched
}

func TestPaletteErrorIsPathQualified(t *testing.T) {
	th := theme.New()
	th.Palette.Dark.Accent = "00ADD8"
	_, err := Compile(th)
	assert.Contains(t, err.Error(), "theme.Palette.Dark.Accent")
}

func TestThemeRoundTripKeepsTemplateSources(t *testing.T) {
	c, err := compileTemplates(t, func(th *theme.Theme) { th.Templates.Choice.Empty = theme.Hidden })
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Editable().Templates.Choice.Empty; got != theme.Hidden {
		t.Fatalf("explicit empty lost in round trip: %+v", got)
	}
	if c.Editable().Templates.Choice.Inactive != "" {
		t.Fatal("inherited slot became explicit")
	}
}
