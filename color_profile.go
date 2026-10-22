// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
)

// Profile is what SGR a destination accepts, ordered by capability.
type Profile int

const (
	// ProfilePlain emits no SGR at all.
	ProfilePlain Profile = iota
	// ProfileAttrs emits bold/italic/underline/dim/strike, but no color.
	ProfileAttrs
	ProfileANSI16
	ProfileANSI256
	ProfileTrueColor
)

const (
	FgDefault = "\x1b[39m"
	// IntensityDefault ends bold and faint.
	IntensityDefault = "\x1b[22m"
	BgDefault        = "\x1b[49m"
)

// Attrs reports whether text attributes are accepted.
func (p Profile) Attrs() bool { return p >= ProfileAttrs }

// Encode returns the foreground or background SGR sequence for c, or nil for
// profiles without color.
func (p Profile) Encode(c SRGB, background bool) Text {
	switch p {
	case ProfileANSI16:
		return Text(c.ansi16Escape(background))
	case ProfileANSI256:
		return fmt.Appendf(nil, "\x1b[%d;5;%dm", c.base(background), c.nearestIndex(256))
	case ProfileTrueColor:
		return fmt.Appendf(nil, "\x1b[%d;2;%d;%d;%dm", c.base(background), c[0], c[1], c[2])
	default:
		return nil
	}
}

// Named preserves a Named SGR code only when the profile supports it.
func (p Profile) Named(seq Text) string {
	set, _ := seq.Scan().SGREffect()
	switch {
	case p >= ProfileANSI16:
		return seq.String()
	case p == ProfileAttrs && set&(SGRForeground|SGRBackground) == 0:
		return seq.String()
	default:
		return ""
	}
}

// Fg returns the SGR sequence selecting c as foreground.
func (p Profile) Fg(c SRGB) Text { return p.Encode(c, false) }

// bg returns the SGR sequence selecting c as background.
func (p Profile) bg(c SRGB) Text { return p.Encode(c, true) }

// Shown is what a destination displays for c: the nearest palette entry for
// indexed profiles, c itself for truecolor.
func (p Profile) Shown(c SRGB) SRGB {
	switch p {
	case ProfileANSI16:
		return ANSIPalette()[c.basicIndex()]
	case ProfileANSI256:
		return ANSIPalette()[c.nearestIndex(256)]
	default:
		return c
	}
}

// Readable returns c, or a nearby shade of the same hue, whose displayed
// color still meets the target against bg. The adjustment is bounded; ok is
// false when no colored result is Readable and the caller should keep a
// neutral color plus a shape or label. The encoder stays pure: only the
// resolved RGB is adjusted, never the encoding. With 16 colors, a shade
// rarely moves to another basic color, so the nearest Readable one of the
// same kind is taken instead.
func (p Profile) Readable(c, bg SRGB, target float64) (rgb SRGB, ok bool) {
	if p == ProfileANSI16 {
		i, ok := c.nearestBasic(func(e SRGB) bool { return e.Contrast(bg) >= target })
		if !ok {
			return c, false
		}
		return ANSIPalette()[i], true
	}
	for step := range 9 {
		cand := c.AtLeast(bg, target+0.5*float64(step))
		if p.Shown(cand).Contrast(bg) >= target {
			return cand, true
		}
	}
	return c, false
}

// distinct drops shades whose encoded output equals an earlier one, so a
// 16-color terminal is not promised five different greens. Comparison uses
// the encoded bytes, never RGB strings. Profiles without color collapse to
// the first shade.
func (p Profile) distinct(shades []SRGB) []SRGB {
	var out []SRGB
	seen := map[string]bool{}
	for _, c := range shades {
		key := string(p.Fg(c))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}
