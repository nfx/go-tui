// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/theme"
	"text/template"
)

// Compiled is an immutable snapshot of an installed theme. Nothing in it
// is derived from a terminal: backgrounds and profiles are bound per operation.
type Compiled struct {
	// Source is the normalized editable configuration, privately owned.
	Source theme.Theme

	// Derived state, computed from source.
	Palette   parsedPalette
	Templates templates
}

// Editable returns the source configuration as an independent copy.
func (c *Compiled) Editable() theme.Theme { return c.Source.Clone() }

// Funcs returns built-in and custom helpers bound to one destination.
func (c *Compiled) Funcs(p color.Profile, roles Roles) template.FuncMap {
	return withCustomFuncs(c.Source, destination{profile: p, roles: roles, symbols: c.Source.Symbols}.funcs())
}

// Geometry binds the theme layout to a terminal size.
func (c *Compiled) Geometry(width, height int) Geometry {
	return Geometry{Inline: c.Source.Inline, Guide: c.Source.Symbols.Guide, Width: width, Height: height}
}

// Roles encodes the semantic Roles of pal for p. Every role carries words, so
// it is held to [contrastText], the guide to [contrastGuide]. A role the
// caller set explicitly renders literally. An automatic neutral keeps the
// terminal default on an assumed background, where it could be nearly
// invisible, unless the theme forces the light variant. A role that no
// displayed shade keeps readable also keeps the terminal default, and the
// symbols of the templates still carry its meaning. A guide without a color
// is faint instead, so it stays quieter than the muted role.
func (c *Compiled) Roles(p color.Profile, pal ResolvedPalette) Roles {
	if p < color.ProfileANSI16 {
		return Roles{}
	}
	v := c.Variant(pal)
	style := func(override slot, rgb color.SRGB, target float64, neutral bool) ansi.Text {
		shown, ok := c.Role(p, pal, override, rgb, target, neutral)
		if !ok {
			return nil
		}
		return p.Fg(shown)
	}
	guide := style(v.guide, pal.Guide, color.ContrastGuide, true)
	if guide == nil {
		guide = ansi.Text(ansi.Dim)
	}
	return Roles{
		Foreground: style(v.foreground, pal.Foreground, color.ContrastText, true),
		Muted:      style(v.Variant, pal.Muted, color.ContrastText, true),
		Guide:      guide,
		Accent:     style(v.Accent, pal.Accent, color.ContrastText, false),
		Success:    style(v.success, pal.Success, color.ContrastText, false),
		Failure:    style(v.err, pal.Error, color.ContrastText, false),
	}
}

// Variant is the parsed palette Variant that pal selected.
func (c *Compiled) Variant(pal ResolvedPalette) parsedVariant {
	if pal.Light {
		return c.Palette.light
	}
	return c.Palette.dark
}

// Role is the color a Role renders in, or false when it keeps the terminal
// default; see [compiledTheme.roles].
func (c *Compiled) Role(
	p color.Profile,
	pal ResolvedPalette,
	override slot,
	rgb color.SRGB,
	target float64,
	neutral bool,
) (color.SRGB, bool) {
	switch {
	case override.set:
		return override.rgb, true
	case neutral && !pal.Known && !c.Source.Light:
		return color.SRGB{}, false
	}
	return p.Readable(rgb, pal.Background, target)
}

// scaleLevels is the number of levels of a diverging scale.
const scaleLevels = 5

// diverging encodes a scale of [scaleLevels] foregrounds from the success
// role through the foreground to the error role. The levels between them
// are halfway in OKLab between the rendered role and the foreground, held to
// [contrastText] like the roles. Levels may encode alike on small palettes;
// an empty level keeps the terminal default.
func (c *Compiled) diverging(p color.Profile, pal ResolvedPalette, roles Roles) []ansi.Text {
	if p < color.ProfileANSI16 {
		return make([]ansi.Text, scaleLevels)
	}
	v := c.Variant(pal)
	half := func(override slot, rgb color.SRGB) ansi.Text {
		end, ok := c.Role(p, pal, override, rgb, color.ContrastText, false)
		if !ok {
			return nil
		}
		mid, ok := p.Readable(end.OKLab().Mix(pal.Foreground.OKLab(), 0.5), pal.Background, color.ContrastText)
		if !ok {
			return nil
		}
		return p.Fg(mid)
	}
	return []ansi.Text{
		roles.Success,
		half(v.success, pal.Success),
		roles.Foreground,
		half(v.err, pal.Error),
		roles.Failure,
	}
}

// Bind resolves the palette against the background of a destination with
// profile p. The variant follows a measured background, or else the theme
// and the estimate in bg.
func (c *Compiled) Bind(p color.Profile, bg color.Background) Binding {
	pal := c.Palette.Resolve(c.Source.Light || bg.Light, bg.RGB, bg.Measured)
	roles := c.Roles(p, pal)
	return Binding{Theme: c, Profile: p, Palette: pal, Roles: roles, Funcs: c.Funcs(p, roles)}
}
