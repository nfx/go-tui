// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"github.com/nfx/go-tui/internal/color"
	"strings"
)

// resolvedPalette is a palette for one destination: the selected variant,
// adjusted for readability against the effective background. It is
// computed from, and never written back to, the caller's [Palette].
type resolvedPalette struct {
	Light      bool // the variant that was selected
	Background color.SRGB
	// Known reports whether Background was measured rather than assumed
	// from the variant's reference background.
	Known bool

	Foreground color.SRGB
	Muted      color.SRGB
	Guide      color.SRGB
	Track      color.SRGB
	Accent     color.SRGB
	Success    color.SRGB
	Error      color.SRGB
}

// slot is a parsed palette color. Set is false when the caller left the field
// empty: a required field then holds its preset, an optional one is absent.
type slot struct {
	rgb color.SRGB
	set bool
}

// parsedVariant is a [PaletteVariant] after parsing and preset filling.
type parsedVariant struct {
	foreground slot
	reference  slot
	accent     slot
	success    slot
	err        slot
	muted      slot
	guide      slot
}

// parsedPalette is a [Palette] after parsing and preset filling.
type parsedPalette struct {
	accent  slot
	success slot
	err     slot
	light   parsedVariant
	dark    parsedVariant
}

// parse fills empty required fields from the preset and parses every color
// once. The error names the theme path of the first malformed value.
func (p Palette) parse() (out parsedPalette, err error) {
	for _, f := range []struct {
		path   string
		c      RGB
		preset RGB // empty for optional fields
		dst    *slot
	}{
		{"Accent", p.Accent, "#00ADD8", &out.accent},
		{"Success", p.Success, "#2D9B55", &out.success},
		{"Error", p.Error, "#C83741", &out.err},
		{"Light.Foreground", p.Light.Foreground, "#232830", &out.light.foreground},
		{"Light.ReferenceBackground", p.Light.ReferenceBackground, "#FFFFFF", &out.light.reference},
		{"Light.Accent", p.Light.Accent, "", &out.light.accent}, {"Light.Success", p.Light.Success, "", &out.light.success},
		{"Light.Error", p.Light.Error, "", &out.light.err}, {"Light.Muted", p.Light.Muted, "", &out.light.muted},
		{"Light.Guide", p.Light.Guide, "", &out.light.guide},
		{"Dark.Foreground", p.Dark.Foreground, "#E1E6EE", &out.dark.foreground},
		{"Dark.ReferenceBackground", p.Dark.ReferenceBackground, "#121212", &out.dark.reference},
		{"Dark.Accent", p.Dark.Accent, "", &out.dark.accent}, {"Dark.Success", p.Dark.Success, "", &out.dark.success},
		{"Dark.Error", p.Dark.Error, "", &out.dark.err}, {"Dark.Muted", p.Dark.Muted, "", &out.dark.muted},
		{"Dark.Guide", p.Dark.Guide, "", &out.dark.guide},
	} {
		src := f.c
		if src == "" {
			src = f.preset
		}
		rgb, err := src.parse()
		if err != nil {
			return out, fmt.Errorf("theme.Palette.%s: %w", f.path, err)
		}
		*f.dst = slot{rgb: rgb, set: f.c != ""}
	}
	return out, nil
}

// resolve selects the variant and derives every role. With a measured
// background (known), the variant follows its luminance; otherwise it is
// light when asked and the variant's reference background is the context.
//
// A shared seed is rendered literally when it meets its contrast target and
// is otherwise moved to the nearest readable lightness of the same hue. A
// per-variant override is always rendered literally, and so is a Foreground
// the caller set; the preset Foreground is moved like a seed. Derived roles
// come from the rendered colors, not from the raw seeds.
func (p Palette) resolve(light bool, measured color.SRGB, known bool) (resolvedPalette, error) {
	pp, err := p.parse()
	if err != nil {
		return resolvedPalette{}, err
	}
	return pp.resolve(light, measured, known), nil
}

// resolve is [Palette.resolve] for a palette that was parsed once, as a
// compiled theme holds it. It cannot fail.
func (pp parsedPalette) resolve(light bool, measured color.SRGB, known bool) resolvedPalette {
	if known {
		light = measured.IsLight()
	}
	v := pp.dark
	if light {
		v = pp.light
	}
	bg := measured
	if !known {
		bg = v.reference.rgb
	}
	fg := v.foreground.rgb
	if !v.foreground.set {
		fg = fg.AtLeast(bg, color.ContrastText)
	}
	seed := func(override, shared slot, target float64) color.SRGB {
		if override.set {
			return override.rgb
		}
		return shared.rgb.AtLeast(bg, target)
	}
	quiet := func(override slot, target float64) color.SRGB {
		if override.set {
			return override.rgb
		}
		return fg.Quieter(bg, target)
	}
	out := resolvedPalette{
		Light:      light,
		Background: bg,
		Known:      known,
		Foreground: fg,
		Accent:     seed(v.accent, pp.accent, color.ContrastText),
		Success:    seed(v.success, pp.success, color.ContrastMark),
		Error:      seed(v.err, pp.err, color.ContrastText),
		Muted:      quiet(v.muted, color.ContrastText),
		Guide:      quiet(v.guide, color.ContrastGuide),
	}
	out.Track = out.Guide
	return out
}

// String is for diagnostics and golden comparisons.
func (r resolvedPalette) String() string {
	var b strings.Builder
	for _, f := range []struct {
		name string
		c    color.SRGB
	}{
		{"fg", r.Foreground}, {"muted", r.Muted}, {"guide", r.Guide},
		{"accent", r.Accent}, {"success", r.Success}, {"error", r.Error},
	} {
		fmt.Fprintf(&b, "%s=%s ", f.name, f.c.Hex())
	}
	return strings.TrimSpace(b.String())
}
