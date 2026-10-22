// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"text/template"
)

// compiledTheme is an immutable snapshot of an installed theme. Nothing in it
// is derived from a terminal: backgrounds and profiles are bound per operation.
type compiledTheme struct {
	// source is the normalized editable configuration, privately owned.
	source Theme

	// Derived state, computed from source.
	palette   parsedPalette
	templates templates
}

// editable returns the source configuration as an independent copy.
func (c *compiledTheme) editable() Theme { return c.source.clone() }

// funcs returns built-in and custom helpers bound to one destination.
func (c *compiledTheme) funcs(p color.Profile, roles roleStyles) template.FuncMap {
	return c.source.withCustom(destination{profile: p, roles: roles, symbols: c.source.Symbols}.funcs())
}

// geometry binds the theme layout to a terminal size.
func (c *compiledTheme) geometry(width, height int) geometry {
	return geometry{inline: c.source.Inline, guide: c.source.Symbols.Guide, width: width, height: height}
}

// roles encodes the semantic roles of pal for p. Every role carries words, so
// it is held to [contrastText], the guide to [contrastGuide]. A role the
// caller set explicitly renders literally. An automatic neutral keeps the
// terminal default on an assumed background, where it could be nearly
// invisible, unless the theme forces the light variant. A role that no
// displayed shade keeps readable also keeps the terminal default, and the
// symbols of the templates still carry its meaning. A guide without a color
// is faint instead, so it stays quieter than the muted role.
func (c *compiledTheme) roles(p color.Profile, pal resolvedPalette) roleStyles {
	if p < color.ProfileANSI16 {
		return roleStyles{}
	}
	v := c.variant(pal)
	style := func(override slot, rgb color.SRGB, target float64, neutral bool) ansi.Text {
		shown, ok := c.role(p, pal, override, rgb, target, neutral)
		if !ok {
			return nil
		}
		return p.Fg(shown)
	}
	guide := style(v.guide, pal.Guide, color.ContrastGuide, true)
	if guide == nil {
		guide = ansi.Text(ansi.Dim)
	}
	return roleStyles{
		foreground: style(v.foreground, pal.Foreground, color.ContrastText, true),
		muted:      style(v.muted, pal.Muted, color.ContrastText, true),
		guide:      guide,
		accent:     style(v.accent, pal.Accent, color.ContrastText, false),
		success:    style(v.success, pal.Success, color.ContrastText, false),
		failure:    style(v.err, pal.Error, color.ContrastText, false),
	}
}

// variant is the parsed palette variant that pal selected.
func (c *compiledTheme) variant(pal resolvedPalette) parsedVariant {
	if pal.Light {
		return c.palette.light
	}
	return c.palette.dark
}

// role is the color a role renders in, or false when it keeps the terminal
// default; see [compiledTheme.roles].
func (c *compiledTheme) role(
	p color.Profile,
	pal resolvedPalette,
	override slot,
	rgb color.SRGB,
	target float64,
	neutral bool,
) (color.SRGB, bool) {
	switch {
	case override.set:
		return override.rgb, true
	case neutral && !pal.Known && !c.source.Light:
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
func (c *compiledTheme) diverging(p color.Profile, pal resolvedPalette, roles roleStyles) []ansi.Text {
	if p < color.ProfileANSI16 {
		return make([]ansi.Text, scaleLevels)
	}
	v := c.variant(pal)
	half := func(override slot, rgb color.SRGB) ansi.Text {
		end, ok := c.role(p, pal, override, rgb, color.ContrastText, false)
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
		roles.success,
		half(v.success, pal.Success),
		roles.foreground,
		half(v.err, pal.Error),
		roles.failure,
	}
}

// bind resolves the palette against the background of a destination with
// profile p. The variant follows a measured background, or else the theme
// and the estimate in bg.
func (c *compiledTheme) bind(p color.Profile, bg color.Background) binding {
	pal := c.palette.resolve(c.source.Light || bg.Light, bg.RGB, bg.Measured)
	roles := c.roles(p, pal)
	return binding{theme: c, profile: p, palette: pal, roles: roles, funcs: c.funcs(p, roles)}
}
