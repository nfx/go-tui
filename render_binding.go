// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"text/template"
)

// binding is a compiled theme bound to the destination of one operation.
// Renderers take helpers, roles and geometry from it, never from the
// installed default, so one operation cannot mix two themes.
type binding struct {
	theme   *compiledTheme
	profile Profile
	palette resolvedPalette
	roles   roleStyles
	// funcs are the helpers of theme slots and projections. Templates
	// only read the map.
	funcs template.FuncMap
}

// geometry is the layout of the theme for a terminal size, with the guide
// in its role color.
func (b binding) geometry(width, height int) geometry {
	g := b.theme.geometry(width, height)
	g.guide = b.style(b.roles.guide, Text(g.guide)).String()
	return g
}

// style wraps renderer-owned content, such as an editable value, in a role.
func (binding) style(role, content Text) Text {
	if len(content) == 0 {
		return content
	}
	return destination{}.roleSpan(role).wrap(content)
}

// diverging is the diverging scale of [compiledTheme.diverging] for the destination.
func (b binding) diverging() []Text { return b.theme.diverging(b.profile, b.palette, b.roles) }
