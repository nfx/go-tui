// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
)

// colorProfile is what SGR a destination accepts, ordered by capability.
type colorProfile int

const (
	// profilePlain emits no SGR at all.
	profilePlain colorProfile = iota
	// profileAttrs emits bold/italic/underline/dim/strike, but no color.
	profileAttrs
	profileANSI16
	profileANSI256
	profileTrueColor
)

const (
	fgDefault = "\x1b[39m"
	// intensityDefault ends bold and faint.
	intensityDefault = "\x1b[22m"
	bgDefault        = "\x1b[49m"
)

// attrs reports whether text attributes are accepted.
func (p colorProfile) attrs() bool { return p >= profileAttrs }

// encode returns the foreground or background SGR sequence for c, or nil for
// profiles without color.
func (p colorProfile) encode(c sRGB, background bool) text {
	switch p {
	case profileANSI16:
		return text(c.ansi16Escape(background))
	case profileANSI256:
		return fmt.Appendf(nil, "\x1b[%d;5;%dm", c.base(background), c.nearestIndex(256))
	case profileTrueColor:
		return fmt.Appendf(nil, "\x1b[%d;2;%d;%d;%dm", c.base(background), c[0], c[1], c[2])
	default:
		return nil
	}
}

// named preserves a named SGR code only when the profile supports it.
func (p colorProfile) named(seq text) string {
	set, _ := seq.scan().sgrEffect()
	switch {
	case p >= profileANSI16:
		return seq.String()
	case p == profileAttrs && set&(sgrForeground|sgrBackground) == 0:
		return seq.String()
	default:
		return ""
	}
}

// fg returns the SGR sequence selecting c as foreground.
func (p colorProfile) fg(c sRGB) text { return p.encode(c, false) }

// bg returns the SGR sequence selecting c as background.
func (p colorProfile) bg(c sRGB) text { return p.encode(c, true) }

// shown is what a destination displays for c: the nearest palette entry for
// indexed profiles, c itself for truecolor.
func (p colorProfile) shown(c sRGB) sRGB {
	switch p {
	case profileANSI16:
		return ansiPalette()[c.basicIndex()]
	case profileANSI256:
		return ansiPalette()[c.nearestIndex(256)]
	default:
		return c
	}
}

// readable returns c, or a nearby shade of the same hue, whose displayed
// color still meets the target against bg. The adjustment is bounded; ok is
// false when no colored result is readable and the caller should keep a
// neutral color plus a shape or label. The encoder stays pure: only the
// resolved RGB is adjusted, never the encoding. With 16 colors, a shade
// rarely moves to another basic color, so the nearest readable one of the
// same kind is taken instead.
func (p colorProfile) readable(c, bg sRGB, target float64) (rgb sRGB, ok bool) {
	if p == profileANSI16 {
		i, ok := c.nearestBasic(func(e sRGB) bool { return e.contrast(bg) >= target })
		if !ok {
			return c, false
		}
		return ansiPalette()[i], true
	}
	for step := range 9 {
		cand := c.atLeast(bg, target+0.5*float64(step))
		if p.shown(cand).contrast(bg) >= target {
			return cand, true
		}
	}
	return c, false
}

// distinct drops shades whose encoded output equals an earlier one, so a
// 16-color terminal is not promised five different greens. Comparison uses
// the encoded bytes, never RGB strings. Profiles without color collapse to
// the first shade.
func (p colorProfile) distinct(shades []sRGB) []sRGB {
	var out []sRGB
	seen := map[string]bool{}
	for _, c := range shades {
		key := string(p.fg(c))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}
