// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

// Palette holds the semantic color seeds of a theme. Foreground and the
// background used for contrast come from the Light or Dark variant that
// matches the terminal; the chromatic seeds are shared between both.
type Palette struct {
	Accent  RGB
	Success RGB
	Error   RGB // shared chromatic seeds
	Light   PaletteVariant
	Dark    PaletteVariant
}

// canonical rewrites specified colors as uppercase #RRGGBB. Empty fields stay
// empty: an inherited value must never become an explicit override.
func (p Palette) canonical() Palette {
	for _, c := range []*RGB{
		&p.Accent, &p.Success, &p.Error,
		&p.Light.Foreground, &p.Light.ReferenceBackground, &p.Light.Accent,
		&p.Light.Success, &p.Light.Error, &p.Light.Muted, &p.Light.Guide,
		&p.Dark.Foreground, &p.Dark.ReferenceBackground, &p.Dark.Accent,
		&p.Dark.Success, &p.Dark.Error, &p.Dark.Muted, &p.Dark.Guide,
	} {
		if *c != "" {
			*c = RGB(c.must().Hex())
		}
	}
	return p
}

// PaletteVariant is the palette for one terminal appearance.
type PaletteVariant struct {
	Foreground          RGB
	ReferenceBackground RGB // used only when the actual background is unknown
	Accent              RGB
	Success             RGB
	Error               RGB // optional exact override of shared seeds
	Muted               RGB
	Guide               RGB // optional overrides of derived neutrals; Track inherits Guide
}
