// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"text/template"
)

// Binding is a compiled theme bound to the destination of one operation.
// Renderers take helpers, roles and geometry from it, never from the
// installed default, so one operation cannot mix two themes.
type Binding struct {
	Theme   *Compiled
	Profile color.Profile
	Palette ResolvedPalette
	Roles   Roles
	// Funcs are the helpers of theme slots and projections. Templates
	// only read the map.
	Funcs template.FuncMap
}

// Geometry is the layout of the theme for a terminal size, with the guide
// in its role color.
func (b Binding) Geometry(width, height int) Geometry {
	g := b.Theme.Geometry(width, height)
	g.Guide = b.Style(b.Roles.Guide, ansi.Text(g.Guide)).String()
	return g
}

// Style wraps renderer-owned content, such as an editable value, in a role.
func (Binding) Style(role, content ansi.Text) ansi.Text {
	if len(content) == 0 {
		return content
	}
	return destination{}.roleSpan(role).wrap(content)
}

// Diverging is the Diverging scale of [compiledTheme.Diverging] for the destination.
func (b Binding) Diverging() []ansi.Text { return b.Theme.diverging(b.Profile, b.Palette, b.Roles) }
