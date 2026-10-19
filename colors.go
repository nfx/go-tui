// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"text/template"
)

const (
	reset = "\x1b[0m"

	// Colors.
	black  = "\x1b[30m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
	purple = "\x1b[35m"
	cyan   = "\x1b[36m"
	white  = "\x1b[37m"

	// Bright colors.
	brightBlack  = "\x1b[90m"
	brightRed    = "\x1b[91m"
	brightGreen  = "\x1b[92m"
	brightYellow = "\x1b[93m"
	brightBlue   = "\x1b[94m"
	brightPurple = "\x1b[95m"
	brightCyan   = "\x1b[96m"
	brightWhite  = "\x1b[97m"

	// Background colors.
	bgBlack  = "\x1b[40m"
	bgRed    = "\x1b[41m"
	bgGreen  = "\x1b[42m"
	bgYellow = "\x1b[43m"
	bgBlue   = "\x1b[44m"
	bgPurple = "\x1b[45m"
	bgCyan   = "\x1b[46m"
	bgWhite  = "\x1b[47m"

	// Text combineting.
	bold          = "\x1b[1m"
	dim           = "\x1b[2m"
	italic        = "\x1b[3m"
	underline     = "\x1b[4m"
	strikethrough = "\x1b[9m"
)

var colorFns = template.FuncMap{
	"black":        ansciiFormatter(black),
	"red":          ansciiFormatter(red),
	"green":        ansciiFormatter(green),
	"yellow":       ansciiFormatter(yellow),
	"blue":         ansciiFormatter(blue),
	"purple":       ansciiFormatter(purple),
	"cyan":         ansciiFormatter(cyan),
	"white":        ansciiFormatter(white),
	"brightBlack":  ansciiFormatter(brightBlack),
	"brightRed":    ansciiFormatter(brightRed),
	"brightGreen":  ansciiFormatter(brightGreen),
	"brightYellow": ansciiFormatter(brightYellow),
	"brightBlue":   ansciiFormatter(brightBlue),
	"brightPurple": ansciiFormatter(brightPurple),
	"brightCyan":   ansciiFormatter(brightCyan),
	"brightWhite":  ansciiFormatter(brightWhite),
	"bgBlack":      ansciiFormatter(bgBlack),
	"bgRed":        ansciiFormatter(bgRed),
	"bgGreen":      ansciiFormatter(bgGreen),
	"bgYellow":     ansciiFormatter(bgYellow),
	"bgBlue":       ansciiFormatter(bgBlue),
	"bgPurple":     ansciiFormatter(bgPurple),
	"bgCyan":       ansciiFormatter(bgCyan),
	"bgWhite":      ansciiFormatter(bgWhite),
	"bold":         ansciiFormatter(bold),
	"dim":          ansciiFormatter(dim),
	"italic":       ansciiFormatter(italic),
	"underline":    ansciiFormatter(underline),
	"strike":       ansciiFormatter(strikethrough),
	"ago":          ago,
}

func WithFn(name string, fn any) {
	// TODO: add validation for fn signature via reflect
	colorFns[name] = fn
}

func ansciiFormatter(codes ...string) func(...any) string {
	return func(text ...any) string {
		var result strings.Builder
		for _, code := range codes {
			result.WriteString(code)
		}
		for _, t := range text {
			switch x := t.(type) {
			case *string:
				if x != nil {
					result.WriteString(*x)
				}
			case []string:
				result.WriteString(strings.Join(x, ", "))
			case *int:
				if x != nil {
					fmt.Fprint(&result, *x)
				}
			default:
				fmt.Fprint(&result, t)
			}
		}
		result.WriteString(reset)
		return result.String()
	}
}

// RGB is a 24-bit sRGB color in CSS hex notation, "#RRGGBB" or the
// shorthand "#RGB". Hex digits are case-insensitive. The empty string
// means unset: the slot inherits or derives its color.
//
// RGB values must not be compared with ==: "#0AD", "#00aadd" and "#00AADD"
// are the same color.
type RGB string

// Validate returns nil when c is empty or well-formed hex.
func (c RGB) Validate() error {
	_, err := c.parse()
	return err
}

// sRGB holds parsed 8-bit sRGB components.
// see https://en.wikipedia.org/wiki/SRGB.
type sRGB [3]uint8

// parse returns the color bytes. The empty (unset) value parses to zero
// bytes without error, so callers that care check c == "" themselves.
func (c RGB) parse() (rgb sRGB, err error) {
	s := string(c)
	if s == "" {
		return rgb, nil
	}
	bad := fmt.Errorf("%q: want \"#RRGGBB\" or \"#RGB\"", s)
	if s[0] != '#' {
		return rgb, bad
	}
	digits := s[1:]
	switch len(digits) {
	case 3:
		for i := range 3 {
			v, ok := c.hexDigit(digits[i])
			if !ok {
				return rgb, bad
			}
			rgb[i] = v<<4 | v
		}
	case 6:
		for i := range 3 {
			hi, ok1 := c.hexDigit(digits[2*i])
			lo, ok2 := c.hexDigit(digits[2*i+1])
			if !ok1 || !ok2 {
				return rgb, bad
			}
			rgb[i] = hi<<4 | lo
		}
	default:
		return rgb, bad
	}
	return rgb, nil
}

func (c RGB) hexDigit(b byte) (uint8, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// hex returns the canonical uppercase "#RRGGBB" form.
func (c sRGB) hex() RGB {
	return RGB(fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2]))
}

// base is the extended-color SGR parameter: 38 for foreground, 48 for background.
func (sRGB) base(background bool) int {
	if background {
		return 48
	}
	return 38
}

// nearestIndex returns the palette index among the first n entries that is
// closest in [oklab]. Ties go to the lowest index. The distance is always
// measured from the original color, so 16-color output is never derived from
// a 256-color approximation.
func (c sRGB) nearestIndex(n int) int {
	target := c.oklab()
	lab := ansiPaletteLab()
	best, bestDist := 0, math.Inf(1)
	for i := range n {
		if d := target.distance(lab[i]); d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// ansi16Escape encodes the nearest of the 16 basic colors. Bright colors use
// the 90/100 series, never bold, so weight and brightness stay independent.
func (c sRGB) ansi16Escape(background bool) string {
	i := c.nearestIndex(16)
	var code int
	switch {
	case i < 8 && background:
		code = 40 + i
	case i < 8:
		code = 30 + i
	case background:
		code = 100 + i - 8
	default:
		code = 90 + i - 8
	}
	return "\x1b[" + strconv.Itoa(code) + "m"
}

// srgbToLinear undoes the sRGB transfer function for one 8-bit component.
func (c sRGB) srgbToLinear(v uint8) float64 {
	f := float64(v) / 255
	if f <= 0.04045 {
		return f / 12.92
	}
	return math.Pow((f+0.055)/1.055, 2.4)
}

// oklab converts to OKLab using Björn Ottosson's matrices.
// see https://bottosson.github.io/posts/oklab/#converting-from-linear-srgb-to-oklab.
func (c sRGB) oklab() oklab {
	r, g, b := c.srgbToLinear(c[0]), c.srgbToLinear(c[1]), c.srgbToLinear(c[2])
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return oklab{
		L: 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		A: 1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		B: 0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

var (
	// ansiPalette returns the assumed RGB table: the 16 conventional entries,
	// the xterm 6x6x6 cube at 16+36r+6g+b, and the 24 grays at 232..255.
	// These are assumptions: terminals may redefine their palette.
	ansiPalette = sync.OnceValue(func() (t [256]sRGB) {
		copy(t[:16], []sRGB{
			{0x00, 0x00, 0x00}, {0x80, 0x00, 0x00}, {0x00, 0x80, 0x00}, {0x80, 0x80, 0x00},
			{0x00, 0x00, 0x80}, {0x80, 0x00, 0x80}, {0x00, 0x80, 0x80}, {0xc0, 0xc0, 0xc0},
			{0x80, 0x80, 0x80}, {0xff, 0x00, 0x00}, {0x00, 0xff, 0x00}, {0xff, 0xff, 0x00},
			{0x00, 0x00, 0xff}, {0xff, 0x00, 0xff}, {0x00, 0xff, 0xff}, {0xff, 0xff, 0xff},
		})
		levels := [6]uint8{0, 95, 135, 175, 215, 255}
		for r := range 6 {
			for g := range 6 {
				for b := range 6 {
					t[16+36*r+6*g+b] = sRGB{levels[r], levels[g], levels[b]}
				}
			}
		}
		for k := range 24 {
			v := uint8(8 + 10*k)
			t[232+k] = sRGB{v, v, v}
		}
		return t
	})

	// ansiPaletteLab caches the OKLab value of every palette entry.
	ansiPaletteLab = sync.OnceValue(func() (t [256]oklab) {
		for i, c := range ansiPalette() {
			t[i] = c.oklab()
		}
		return t
	})
)

// oklab is a color in the OKLab space.
// see https://bottosson.github.io/posts/oklab/ for more info.
type oklab struct{ L, A, B float64 }

// distance is the squared Euclidean distance.
func (c oklab) distance(o oklab) float64 {
	dl, da, db := c.L-o.L, c.A-o.A, c.B-o.B
	return dl*dl + da*da + db*db
}

// colorProfile encodes c as a foreground or background SGR sequence for one
// destination capability. Profiles without color return "".
type colorProfile func(c sRGB, background bool) string

const (
	fgDefault = "\x1b[39m"
	bgDefault = "\x1b[49m"
)

var (
	// profilePlain emits no SGR at all.
	profilePlain colorProfile = func(sRGB, bool) string { return "" }
	// profileAttrs emits text attributes (bold, ...) but no color; the
	// attributes themselves are not part of the color encoder.
	profileAttrs colorProfile = func(sRGB, bool) string { return "" }

	profileANSI16 colorProfile = func(c sRGB, background bool) string {
		return c.ansi16Escape(background)
	}
	profileANSI256 colorProfile = func(c sRGB, background bool) string {
		return fmt.Sprintf("\x1b[%d;5;%dm", c.base(background), c.nearestIndex(256))
	}
	profileTrueColor colorProfile = func(c sRGB, background bool) string {
		return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", c.base(background), c[0], c[1], c[2])
	}
)

// fg returns the SGR sequence selecting c as foreground.
func (p colorProfile) fg(c sRGB) string { return p(c, false) }

// bg returns the SGR sequence selecting c as background.
func (p colorProfile) bg(c sRGB) string { return p(c, true) }
