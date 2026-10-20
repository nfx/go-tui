// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"unicode"
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

// must parses a color that was already validated or is a constant.
func (c RGB) must() sRGB {
	rgb, err := c.parse()
	if err != nil {
		panic(err)
	}
	return rgb
}

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

// contrast is the WCAG contrast ratio between two colors, in 1..21.
func (c sRGB) contrast(o sRGB) float64 {
	a, b := c.luminance(), o.luminance()
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// luminance is the linear-sRGB relative luminance.
func (c sRGB) luminance() float64 {
	return 0.2126*c.srgbToLinear(c[0]) + 0.7152*c.srgbToLinear(c[1]) + 0.0722*c.srgbToLinear(c[2])
}

// isLight classifies a background as light, at or above [lightBoundary].
func (c sRGB) isLight() bool { return c.luminance() >= lightBoundary }

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

// atLeast returns c unchanged when it has at least the target contrast
// against bg. Otherwise it returns the same hue at the smallest lightness
// change that does, in whichever direction needs less. When neither black nor
// white suffices, the one with more contrast is returned.
func (c sRGB) atLeast(bg sRGB, target float64) sRGB {
	if c.contrast(bg) >= target {
		return c
	}
	lch := c.oklab().lch()
	var best sRGB
	bestStep, found := math.Inf(1), false
	for _, far := range []float64{0, 1} {
		l, ok := lch.reach(bg, target, far)
		if !ok {
			continue
		}
		if step := math.Abs(l - lch.L); step < bestStep {
			best, bestStep, found = lch.withLightness(l), step, true
		}
	}
	if found {
		return best
	}
	black, white := lch.withLightness(0), lch.withLightness(1)
	if black.contrast(bg) >= white.contrast(bg) {
		return black
	}
	return white
}

// atContrast is the color of c's hue closest to bg that reaches the target
// contrast, moving away from bg toward black on light backgrounds and toward
// white on dark ones. It may be less contrasty than c itself. When even the
// extreme falls short, that extreme is returned.
func (c sRGB) atContrast(bg sRGB, target float64) sRGB {
	far := 1.0
	if bg.isLight() {
		far = 0.0
	}
	lch := c.oklab().lch()
	l, _ := lch.reach(bg, target, far)
	return lch.withLightness(l)
}

// reach finds the lightness nearest bg's, on the side of far, whose color
// reaches the target contrast. When even far falls short, it returns far and
// false.
func (c oklch) reach(bg sRGB, target, far float64) (float64, bool) {
	if c.withLightness(far).contrast(bg) < target {
		return far, false
	}
	// contrast grows as lightness moves away from the background
	return bisect(far, bg.oklab().L, func(l float64) bool {
		return c.withLightness(l).contrast(bg) >= target
	}), true
}

// quieter returns the color between c and bg closest to bg that still has
// the target contrast. When c already falls short, c is returned.
func (c sRGB) quieter(bg sRGB, target float64) sRGB {
	if c.contrast(bg) <= target {
		return c
	}
	a, b := c.oklab(), bg.oklab()
	// 0 is c, 1 is bg
	return a.mix(b, bisect(0, 1, func(t float64) bool {
		return a.mix(b, t).contrast(bg) >= target
	}))
}

// bisect narrows the interval between good, where ok holds, and bad, where
// it does not, and returns the boundary point on the good side.
func bisect(good, bad float64, ok func(float64) bool) float64 {
	for range 32 {
		mid := (good + bad) / 2
		if ok(mid) {
			good = mid
		} else {
			bad = mid
		}
	}
	return good
}

// ramp returns n shades of base's hue with ascending contrast against bg,
// from [contrastMark] to [contrastRampMax]. One level is the base itself.
func (c sRGB) ramp(bg sRGB, n int) ([]sRGB, error) {
	if n < 1 || n > maxRampLevels {
		return nil, fmt.Errorf("levels %d: want 1..%d", n, maxRampLevels)
	}
	if n == 1 {
		return []sRGB{c}, nil
	}
	out := make([]sRGB, n)
	for i := range n {
		target := contrastMark + (contrastRampMax-contrastMark)*float64(i)/float64(n-1)
		out[i] = c.atContrast(bg, target)
	}
	return out, nil
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

// oklch is a color in the cylindrical form of OKLab: lightness, chroma, hue.
type oklch struct{ L, C, H float64 }

func (c oklab) lch() oklch {
	return oklch{L: c.L, C: math.Hypot(c.A, c.B), H: math.Atan2(c.B, c.A)}
}

// mix moves from c toward o by t in 0..1.
func (c oklab) mix(o oklab, t float64) sRGB {
	rgb, _ := oklab{L: c.L + (o.L-c.L)*t, A: c.A + (o.A-c.A)*t, B: c.B + (o.B-c.B)*t}.srgb()
	return rgb
}

// linearToSRGB applies the sRGB transfer function and quantizes to a byte.
func (c oklab) linearToSRGB(v float64) (uint8, bool) {
	const eps = 1e-6
	inGamut := v >= -eps && v <= 1+eps
	v = min(1, max(0, v))
	if v <= 0.0031308 {
		v *= 12.92
	} else {
		v = 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return uint8(math.Round(v * 255)), inGamut
}

// srgb converts back to bytes, reporting whether the color fits the gamut.
// see https://bottosson.github.io/posts/oklab/#converting-from-linear-srgb-to-oklab.
func (c oklab) srgb() (rgb sRGB, inGamut bool) {
	cube := func(v float64) float64 { return v * v * v }
	l := cube(c.L + 0.3963377774*c.A + 0.2158037573*c.B)
	m := cube(c.L - 0.1055613458*c.A - 0.0638541728*c.B)
	s := cube(c.L - 0.0894841775*c.A - 1.2914855480*c.B)
	lin := [3]float64{
		4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s,
	}
	inGamut = true
	for i, v := range lin {
		var ok bool
		rgb[i], ok = c.linearToSRGB(v)
		inGamut = inGamut && ok
	}
	return rgb, inGamut
}

// srgb converts to bytes at the given lightness, keeping the hue and
// reducing chroma only as far as needed to fit sRGB.
func (c oklch) srgb() sRGB {
	at := func(chroma float64) (sRGB, bool) {
		return oklab{L: c.L, A: chroma * math.Cos(c.H), B: chroma * math.Sin(c.H)}.srgb()
	}
	if rgb, ok := at(c.C); ok {
		return rgb
	}
	rgb, _ := at(bisect(0, c.C, func(chroma float64) bool {
		_, ok := at(chroma)
		return ok
	}))
	return rgb
}

// withLightness keeps hue and (as far as the gamut allows) chroma.
func (c oklch) withLightness(l float64) sRGB {
	c.L = l
	return c.srgb()
}

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
	bgDefault = "\x1b[49m"
)

// attrs reports whether text attributes are accepted.
func (p colorProfile) attrs() bool { return p >= profileAttrs }

// bold wraps s in bold SGR when the profile allows attributes.
func (p colorProfile) bold(s string) text {
	if !p.attrs() {
		return text(s)
	}
	return text(bold + s + reset)
}

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

// fg returns the SGR sequence selecting c as foreground.
func (p colorProfile) fg(c sRGB) text { return p.encode(c, false) }

// bg returns the SGR sequence selecting c as background.
func (p colorProfile) bg(c sRGB) text { return p.encode(c, true) }

// shown is what a destination displays for c: the nearest palette entry for
// indexed profiles, c itself for truecolor.
func (p colorProfile) shown(c sRGB) sRGB {
	switch p {
	case profileANSI16:
		return ansiPalette()[c.nearestIndex(16)]
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
// resolved RGB is adjusted, never the encoding.
func (p colorProfile) readable(c, bg sRGB, target float64) (rgb sRGB, ok bool) {
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

// Contrast targets, see https://www.w3.org/WAI/WCAG21/Techniques/general/G18.
const (
	// contrastText is for roles that carry words: labels, keys, reasons.
	contrastText = 4.5
	// contrastMark is for glyphs and markers.
	contrastMark = 3.0
	// contrastGuide is for nonessential structure, which may be quiet.
	contrastGuide = 2.0
	// contrastRampMax is the strongest level of a shade ramp.
	contrastRampMax = 7.0

	// maxRampLevels bounds a shade ramp.
	maxRampLevels = 16

	// lightBoundary is the relative luminance where ideal black and white
	// have equal contrast: sqrt(0.05*1.05) - 0.05. A design choice rather
	// than a terminal protocol rule.
	lightBoundary = 0.1791287847
)

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
			*c = c.must().hex()
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

// resolvedPalette is a palette for one destination: the selected variant,
// adjusted for readability against the effective background. It is
// computed from, and never written back to, the caller's [Palette].
type resolvedPalette struct {
	Light      bool // the variant that was selected
	Background sRGB
	// Known reports whether Background was measured rather than assumed
	// from the variant's reference background.
	Known bool

	Foreground sRGB
	Muted      sRGB
	Guide      sRGB
	Track      sRGB
	Accent     sRGB
	Success    sRGB
	Error      sRGB
}

// slot is a parsed palette color. Set is false when the caller left the field
// empty: a required field then holds its preset, an optional one is absent.
type slot struct {
	rgb sRGB
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
func (p Palette) resolve(light bool, measured sRGB, known bool) (resolvedPalette, error) {
	pp, err := p.parse()
	if err != nil {
		return resolvedPalette{}, err
	}
	return pp.resolve(light, measured, known), nil
}

// resolve is [Palette.resolve] for a palette that was parsed once, as a
// compiled theme holds it. It cannot fail.
func (pp parsedPalette) resolve(light bool, measured sRGB, known bool) resolvedPalette {
	if known {
		light = measured.isLight()
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
		fg = fg.atLeast(bg, contrastText)
	}
	seed := func(override, shared slot, target float64) sRGB {
		if override.set {
			return override.rgb
		}
		return shared.rgb.atLeast(bg, target)
	}
	quiet := func(override slot, target float64) sRGB {
		if override.set {
			return override.rgb
		}
		return fg.quieter(bg, target)
	}
	out := resolvedPalette{
		Light:      light,
		Background: bg,
		Known:      known,
		Foreground: fg,
		Accent:     seed(v.accent, pp.accent, contrastText),
		Success:    seed(v.success, pp.success, contrastMark),
		Error:      seed(v.err, pp.err, contrastText),
		Muted:      quiet(v.muted, contrastText),
		Guide:      quiet(v.guide, contrastGuide),
	}
	out.Track = out.Guide
	return out
}

// String is for diagnostics and golden comparisons.
func (r resolvedPalette) String() string {
	var b strings.Builder
	for _, f := range []struct {
		name string
		c    sRGB
	}{
		{"fg", r.Foreground}, {"muted", r.Muted}, {"guide", r.Guide},
		{"accent", r.Accent}, {"success", r.Success}, {"error", r.Error},
	} {
		fmt.Fprintf(&b, "%s=%s ", f.name, f.c.hex())
	}
	return strings.TrimSpace(b.String())
}

// Theme is the editable configuration of the appearance of every widget. A
// preset such as [GuidedTheme] is a complete, usable theme to edit.
type Theme struct {
	// Light forces the light palette variant without detection. Otherwise the
	// terminal background is detected, falling back to dark.
	Light   bool
	Palette Palette
	Symbols Symbols
}

// clone returns an independent copy. Theme holds only value types today; copy
// every map and slice here when one is added.
func (t Theme) clone() Theme { return t }

// compile validates and compiles t without terminal I/O or any user
// code execution. The caller's value is not modified or retained.
func (in Theme) compile() (*compiledTheme, error) {
	t := in.clone()
	var err error
	c := &compiledTheme{}
	c.palette, err = t.Palette.parse()
	if err != nil {
		return nil, fmt.Errorf("palette: %w", err)
	}
	t.Palette = t.Palette.canonical()
	t.Symbols, err = t.Symbols.normalize()
	if err != nil {
		return nil, fmt.Errorf("symbols: %w", err)
	}
	c.source = t
	return c, nil
}

// Symbols are the glyphs the built-in templates and renderers use. Every
// symbol is a single display column. An empty field takes the Unicode preset.
type Symbols struct {
	Prompt    string
	Focus     string
	Guide     string
	End       string
	Checked   string
	Unchecked string
	Success   string
	Error     string
	Cancel    string
	Ellipsis  string
	Mask      string
}

// unicodeSymbols is the preset every empty symbol falls back to.
var unicodeSymbols = Symbols{
	Prompt:    "◆",
	Focus:     "❯",
	Guide:     "│",
	End:       "└",
	Checked:   "◉",
	Unchecked: "○",
	Success:   "✔",
	Error:     "✖",
	Cancel:    "■",
	Ellipsis:  "…",
	Mask:      "*",
}

func (s Symbols) normalize() (Symbols, error) {
	def := unicodeSymbols
	for _, f := range []struct {
		path string
		dst  *string
		def  *string
	}{
		{"Prompt", &s.Prompt, &def.Prompt},
		{"Focus", &s.Focus, &def.Focus},
		{"Guide", &s.Guide, &def.Guide},
		{"End", &s.End, &def.End},
		{"Checked", &s.Checked, &def.Checked},
		{"Unchecked", &s.Unchecked, &def.Unchecked},
		{"Success", &s.Success, &def.Success},
		{"Error", &s.Error, &def.Error},
		{"Cancel", &s.Cancel, &def.Cancel},
		{"Ellipsis", &s.Ellipsis, &def.Ellipsis},
		{"Mask", &s.Mask, &def.Mask},
	} {
		if *f.dst == "" {
			*f.dst = *f.def
		}
		err := s.singleColumn(*f.dst)
		if err != nil {
			return s, fmt.Errorf("theme.Symbols.%s: %w", f.path, err)
		}
	}
	return s, nil
}

// singleColumn checks a glyph that must occupy exactly one display column.
func (Symbols) singleColumn(s string) error {
	if s == "" {
		return errors.New("must not be empty")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("%q: control character", s)
		}
	}
	w := text(s).width()
	if w != 1 || len(text(s).clusters()) != 1 {
		return fmt.Errorf("%q: want one display column, got %d", s, w)
	}
	return nil
}

// ASCIISymbols is the symbol set for terminals whose fonts lack the Unicode
// glyphs.
func ASCIISymbols() Symbols {
	return Symbols{
		Prompt:    "?",
		Focus:     ">",
		Guide:     "|",
		End:       "`",
		Checked:   "x",
		Unchecked: "o",
		Success:   "+",
		Error:     "!",
		Cancel:    "-",
		Ellipsis:  ".",
		Mask:      "*",
	}
}

// presetTheme builds the default theme. The palette stays empty: [Palette.parse]
// fills every required color, and leaving the adaptive neutrals unset keeps
// them adapting to the background instead of becoming exact overrides.
func presetTheme() Theme {
	return Theme{
		Palette: Palette{},
		Symbols: unicodeSymbols,
	}
}

// GuidedTheme returns an independent, editable copy of the Guided preset.
func GuidedTheme() Theme { return presetTheme() }

// compiledTheme is an immutable snapshot of an installed theme. Nothing in it
// is derived from a terminal: backgrounds and profiles are bound per operation.
type compiledTheme struct {
	// source is the normalized editable configuration, privately owned.
	source Theme

	// Derived state, computed from source.
	palette parsedPalette
}

// editable returns the source configuration as an independent copy.
func (c *compiledTheme) editable() Theme { return c.source.clone() }
