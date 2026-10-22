// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
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
func (c *compiledTheme) funcs(p colorProfile, roles roleStyles) template.FuncMap {
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
func (c *compiledTheme) roles(p colorProfile, pal resolvedPalette) roleStyles {
	if p < profileANSI16 {
		return roleStyles{}
	}
	v := c.variant(pal)
	style := func(override slot, rgb sRGB, target float64, neutral bool) text {
		shown, ok := c.role(p, pal, override, rgb, target, neutral)
		if !ok {
			return nil
		}
		return p.fg(shown)
	}
	guide := style(v.guide, pal.Guide, contrastGuide, true)
	if guide == nil {
		guide = text(dim)
	}
	return roleStyles{
		foreground: style(v.foreground, pal.Foreground, contrastText, true),
		muted:      style(v.muted, pal.Muted, contrastText, true),
		guide:      guide,
		accent:     style(v.accent, pal.Accent, contrastText, false),
		success:    style(v.success, pal.Success, contrastText, false),
		failure:    style(v.err, pal.Error, contrastText, false),
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
	p colorProfile,
	pal resolvedPalette,
	override slot,
	rgb sRGB,
	target float64,
	neutral bool,
) (sRGB, bool) {
	switch {
	case override.set:
		return override.rgb, true
	case neutral && !pal.Known && !c.source.Light:
		return sRGB{}, false
	}
	return p.readable(rgb, pal.Background, target)
}

// scaleLevels is the number of levels of a diverging scale.
const scaleLevels = 5

// diverging encodes a scale of [scaleLevels] foregrounds from the success
// role through the foreground to the error role. The levels between them
// are halfway in OKLab between the rendered role and the foreground, held to
// [contrastText] like the roles. Levels may encode alike on small palettes;
// an empty level keeps the terminal default.
func (c *compiledTheme) diverging(p colorProfile, pal resolvedPalette, roles roleStyles) []text {
	if p < profileANSI16 {
		return make([]text, scaleLevels)
	}
	v := c.variant(pal)
	half := func(override slot, rgb sRGB) text {
		end, ok := c.role(p, pal, override, rgb, contrastText, false)
		if !ok {
			return nil
		}
		mid, ok := p.readable(end.oklab().mix(pal.Foreground.oklab(), 0.5), pal.Background, contrastText)
		if !ok {
			return nil
		}
		return p.fg(mid)
	}
	return []text{
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
func (c *compiledTheme) bind(p colorProfile, bg background) binding {
	pal := c.palette.resolve(c.source.Light || bg.light, bg.rgb, bg.measured)
	roles := c.roles(p, pal)
	return binding{theme: c, profile: p, palette: pal, roles: roles, funcs: c.funcs(p, roles)}
}
