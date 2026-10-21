// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"
	"unicode"
)

const (
	reset = "\x1b[0m"

	// Text attributes.
	bold          = "\x1b[1m"
	dim           = "\x1b[2m"
	italic        = "\x1b[3m"
	underline     = "\x1b[4m"
	strikethrough = "\x1b[9m"
)

// Go brand colors, from the Go Brand Book v1.0, section 2.1 "color", page 16.
// They seed the palette presets and stay unexported: docs and examples write
// hex. Each preset documents the role it plays in themes.md.
const (
	// presetSuccess is not a brand color: Aqua failed the distinctness gate.
	presetSuccess = "#2D9B55"

	brandGopherBlue = "#00ADD8" // primary: Accent
	brandDarkBlue   = "#00758D" // "more" colors: Accent on light backgrounds
	brandAqua       = "#00A29C" // primary: rejected as Success, see TestAquaSuccessGate
	brandFuchsia    = "#CE3262" // secondary: Error
	brandBlack      = "#000000" // Foreground on light backgrounds
	brandCoolGray   = "#DBD9D6" // Foreground on dark backgrounds
)

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
	// preset marks an unset optional field that supplies its variant's seed.
	// It is moved to a readable shade like the shared seed, never literal.
	preset bool
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
		{"Accent", p.Accent, brandGopherBlue, &out.accent},
		{"Success", p.Success, presetSuccess, &out.success},
		{"Error", p.Error, brandFuchsia, &out.err},
		{"Light.Foreground", p.Light.Foreground, brandBlack, &out.light.foreground},
		{"Light.ReferenceBackground", p.Light.ReferenceBackground, "#FFFFFF", &out.light.reference},
		{"Light.Accent", p.Light.Accent, "", &out.light.accent}, {"Light.Success", p.Light.Success, "", &out.light.success},
		{"Light.Error", p.Light.Error, "", &out.light.err}, {"Light.Muted", p.Light.Muted, "", &out.light.muted},
		{"Light.Guide", p.Light.Guide, "", &out.light.guide},
		{"Dark.Foreground", p.Dark.Foreground, brandCoolGray, &out.dark.foreground},
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
	// The brand's dark blue reads better on light backgrounds than a computed
	// shade of the seed. It replaces the seed only while the caller left both
	// the seed and the variant's accent empty, and it is never an override.
	if p.Accent == "" && p.Light.Accent == "" {
		out.light.accent = slot{rgb: RGB(brandDarkBlue).must(), preset: true}
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
		if override.preset {
			return override.rgb.atLeast(bg, target)
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
// preset such as [NewTheme] is a complete, usable theme to edit.
type Theme struct {
	// Light forces the light palette variant without detection. Otherwise the
	// terminal background is detected, falling back to dark.
	Light bool
	// Inline keeps each prompt's label on its value row. Otherwise the label
	// has its own row, and the value, list and feedback rows start with the
	// guide symbol. Only geometry differs.
	Inline    bool
	Palette   Palette
	Symbols   Symbols
	Templates Templates
	Spinner   SpinnerStyle
	Progress  ProgressStyle
	Table     TableStyle
	JSON      JSONStyle
	// Funcs are custom template helpers, added to the built-in ones. They
	// cannot replace a built-in helper, and must be safe for concurrent use.
	Funcs template.FuncMap
}

// clone copies theme configuration without isolating helper closures.
func (t Theme) clone() Theme {
	t.Funcs = maps.Clone(t.Funcs)
	t.Symbols.Spinner = slices.Clone(t.Symbols.Spinner)
	return t
}

// withCustom adds custom helpers to builtins in place.
func (t Theme) withCustom(builtins template.FuncMap) template.FuncMap {
	maps.Copy(builtins, t.Funcs)
	return builtins
}

// compile validates an independent theme copy without terminal I/O or executing user code.
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
	t.Spinner, err = t.Spinner.normalize()
	if err != nil {
		return nil, err
	}
	t.Progress, err = t.Progress.normalize()
	if err != nil {
		return nil, err
	}
	t.Table, err = t.Table.normalize()
	if err != nil {
		return nil, err
	}
	t.JSON, err = t.JSON.normalize()
	if err != nil {
		return nil, err
	}
	builtins := destination{symbols: t.Symbols}.funcs()
	err = in.compileFuncs(builtins, t.Funcs)
	if err != nil {
		return nil, err
	}
	funcs := t.withCustom(builtins)
	base, err := builtinTemplates.compile(funcs, templates{})
	if err != nil {
		return nil, fmt.Errorf("built-in %w", err)
	}
	c.templates, err = t.Templates.compile(funcs, base)
	if err != nil {
		return nil, err
	}
	c.source = t
	return c, nil
}

// compileFuncs reports custom helper validation errors by name.
func (in Theme) compileFuncs(builtins, funcs template.FuncMap) error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(funcs)) {
		if err := in.checkFunc(builtins, name, funcs[name]); err != nil {
			errs = append(errs, fmt.Errorf("theme.Funcs.%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// widgetHelpers are the names of helpers that widgets add to the builtins.
var widgetHelpers = []string{"tableString"}

// generatedHelperPrefixes are the prefixes of names that tables generate.
var generatedHelperPrefixes = []string{"__", "tableColumnTypeFormat", "tableColumnNameFormat"}

// checkFunc validates helper names and signatures without executing user code.
func (in Theme) checkFunc(builtins template.FuncMap, name string, fn any) error {
	if !in.isHelperName(name) {
		return errors.New("not a template identifier")
	}
	if _, ok := builtins[name]; ok || slices.Contains(widgetHelpers, name) {
		return errors.New("reserved for a built-in helper")
	}
	for _, prefix := range generatedHelperPrefixes {
		if strings.HasPrefix(name, prefix) {
			return fmt.Errorf("prefix %q is reserved for generated helpers", prefix)
		}
	}
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func || v.IsNil() {
		return fmt.Errorf("want a function, got %T", fn)
	}
	t := v.Type()
	switch {
	case t.NumOut() == 1:
	case t.NumOut() == 2 && t.Out(1) == reflect.TypeFor[error]():
	default:
		return fmt.Errorf("%s: want one result, or a result and an error", t)
	}
	return nil
}

// isHelperName reports whether name is a template function identifier.
func (in Theme) isHelperName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_':
		case i == 0 && !unicode.IsLetter(r):
			return false
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			return false
		}
	}
	return true
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
	// Up and Down label the arrow keys in the key hints.
	Up   string
	Down string
	Mask string
	// BarFill draws the done cells of a progress bar, BarHead the partly
	// done cell after them, and BarEmpty the rest of its track.
	BarFill  string
	BarHead  string
	BarEmpty string
	// Spinner are the frames of a running spinner, in order. Each frame is
	// a symbol, so it is a single display column like the others. Empty
	// takes the preset.
	Spinner []string
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
	Up:        "↑",
	Down:      "↓",
	Mask:      "*",
	BarFill:   "█",
	BarHead:   "█",
	BarEmpty:  "░",
	Spinner:   []string{"▘", "▀", "▝", "▐", "▗", "▄", "▖", "▌"},
}

// clone copies the symbols, so that edits never reach the preset.
func (s Symbols) clone() Symbols {
	s.Spinner = slices.Clone(s.Spinner)
	return s
}

// named returns the glyph of a field by name.
func (s Symbols) named(name string) (string, error) {
	switch name {
	case "Prompt":
		return s.Prompt, nil
	case "Focus":
		return s.Focus, nil
	case "Guide":
		return s.Guide, nil
	case "End":
		return s.End, nil
	case "Checked":
		return s.Checked, nil
	case "Unchecked":
		return s.Unchecked, nil
	case "Success":
		return s.Success, nil
	case "Error":
		return s.Error, nil
	case "Cancel":
		return s.Cancel, nil
	case "Ellipsis":
		return s.Ellipsis, nil
	case "Up":
		return s.Up, nil
	case "Down":
		return s.Down, nil
	case "Mask":
		return s.Mask, nil
	case "BarFill":
		return s.BarFill, nil
	case "BarHead":
		return s.BarHead, nil
	case "BarEmpty":
		return s.BarEmpty, nil
	}
	return "", fmt.Errorf("symbol %q: no such symbol", name)
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
		{"Up", &s.Up, &def.Up},
		{"Down", &s.Down, &def.Down},
		{"Mask", &s.Mask, &def.Mask},
		{"BarFill", &s.BarFill, &def.BarFill},
		{"BarHead", &s.BarHead, &def.BarHead},
		{"BarEmpty", &s.BarEmpty, &def.BarEmpty},
	} {
		if *f.dst == "" {
			*f.dst = *f.def
		}
		err := s.singleColumn(*f.dst)
		if err != nil {
			return s, fmt.Errorf("theme.Symbols.%s: %w", f.path, err)
		}
	}
	if len(s.Spinner) == 0 {
		s.Spinner = def.Spinner
	}
	s.Spinner = slices.Clone(s.Spinner)
	for i, frame := range s.Spinner {
		err := s.singleColumn(frame)
		if err != nil {
			return s, fmt.Errorf("theme.Symbols.Spinner[%d]: %w", i, err)
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
		Up:        "^",
		Down:      "v",
		Mask:      "*",
		BarFill:   "=",
		BarHead:   ">",
		BarEmpty:  "-",
		// the line spinner of cli-spinners (MIT License)
		Spinner: []string{"|", "/", "-", `\`},
	}
}

// presetTheme builds the default theme. The palette stays empty: [Palette.parse]
// fills every required color, and leaving the adaptive neutrals unset keeps
// them adapting to the background instead of becoming exact overrides.
func presetTheme() Theme {
	return Theme{
		Palette:  Palette{},
		Symbols:  unicodeSymbols.clone(),
		Spinner:  SpinnerStyle{Interval: spinnerInterval},
		Progress: progressStyle,
		Table:    tableStyle,
		JSON:     jsonStyle,
	}
}

// NewTheme returns an editable copy of the built-in theme with adaptive colors.
func NewTheme() Theme { return presetTheme() }

// DefaultTheme returns an editable copy of the installed theme, preserving helpers and unset colors.
func DefaultTheme() Theme { return defaultIO.currentTheme().editable() }

// SetDefaultTheme validates and copies theme for operations started afterwards.
// On error, the installed theme is unchanged; running operations keep their snapshot.
// Funcs replaces existing helpers; start from [DefaultTheme] to retain them.
// Helper closures must be safe for concurrent use.
func SetDefaultTheme(theme Theme) error { return defaultIO.installTheme(theme) }

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
// symbols of the templates still carry its meaning.
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
	return roleStyles{
		foreground: style(v.foreground, pal.Foreground, contrastText, true),
		muted:      style(v.muted, pal.Muted, contrastText, true),
		guide:      style(v.guide, pal.Guide, contrastGuide, true),
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

// binding is a compiled theme bound to the destination of one operation.
// Renderers take helpers, roles and geometry from it, never from the
// installed default, so one operation cannot mix two themes.
type binding struct {
	theme   *compiledTheme
	profile colorProfile
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
	g.guide = b.style(b.roles.guide, text(g.guide)).String()
	return g
}

// style wraps renderer-owned content, such as an editable value, in a role.
func (binding) style(role, content text) text {
	if len(content) == 0 {
		return content
	}
	return destination{}.newSpan(fgDefault, role).wrap(content)
}

// diverging is the diverging scale of [compiledTheme.diverging] for the destination.
func (b binding) diverging() []text { return b.theme.diverging(b.profile, b.palette, b.roles) }

// Template is an optional template source. The zero value inherits from the
// parent slot; [Hidden] is an explicit empty source that hides the slot
// instead of falling back to a parent.
type Template string

// Hidden deliberately hides a slot. It is a sentinel, not template text.
const Hidden Template = "\x00hidden"

// source returns the template text; it is empty for [Hidden].
func (t Template) source() string {
	if t == Hidden {
		return ""
	}
	return string(t)
}

// templated is the input of one compile step: the source slots Src, named
// Name, inheriting unset slots from the compiled Parent.
type templated[A, B any] struct {
	Funcs  template.FuncMap
	Name   string
	Src    A
	Parent B
}

// nest preserves helpers and qualifies nested slots with nonempty field names.
func (in *templated[A, B]) nest[C, D any](field string, src C, parent D) templated[C, D] {
	name := in.Name
	switch {
	case name == "":
		name = field
	case field != "":
		name += "." + field
	}
	return templated[C, D]{Funcs: in.Funcs, Name: name, Src: src, Parent: parent}
}

// Templates are the typed presentation slots of a theme. An unset slot
// inherits from its parent family and finally from the built-in default, so a
// shared edit still reaches every family that has not overridden it.
type Templates struct {
	Prompt PromptTemplates
	Choice ChoiceTemplates

	Input    PromptTemplates
	Password SecretTemplates
	Number   SecretTemplates

	Dropdown    SelectTemplates
	Multichoice MultichoiceTemplates
	Confirm     SelectTemplates
	FilePicker  SelectTemplates
	Spinner     SpinnerTemplates
	Progress    ProgressTemplates
	Table       TableTemplates
}

// templates are the compiled [Templates], with inheritance resolved.
type templates struct {
	Prompt promptTemplates
	Choice choiceTemplates

	Input    promptTemplates
	Password secretTemplates
	Number   secretTemplates

	Dropdown    choiceFamily
	Multichoice multichoiceTemplates
	Confirm     choiceFamily
	FilePicker  choiceFamily

	Spinner  spinnerTemplates
	Progress progressTemplates
	Table    tableTemplates
}

// builtinTemplates are the built-in sources of the root slots, shared by
// Guided and Inline: only geometry differs. Family slots stay unset, so that
// they inherit, and a root slot without a source has no built-in yet.
var builtinTemplates = Templates{
	Prompt: PromptTemplates{
		Label:  `{{ accent (symbol "Prompt") }} {{ bold (text .Label) }}`,
		Answer: `{{ success (symbol "Success") }} {{ text .Label }} {{ muted "·" }} {{ text .Value }}`,
		Hint: `{{ range $i, $b := .Bindings }}{{ if $i }}{{ muted " · " }}{{ end }}` +
			`{{ accent (keycap $b.Key) }} {{ muted $b.Action }}{{ end }}`,
		Feedback: `{{ error (symbol "Error") }} {{ error .Message }}`,
	},
	Choice: ChoiceTemplates{
		Value:    `{{ label . }}`,
		Details:  Hidden,
		Active:   `{{ bold (accent .Value) }}{{ with .Details }} {{ muted . }}{{ end }}`,
		Inactive: `{{ text .Value }}`,
		More:     `{{ muted (printf "%s %d more" (symbol "Ellipsis") .More) }}`,
		Empty:    `{{ muted "No matches" }}`,
	},
	Multichoice: MultichoiceTemplates{
		Row: `{{ if .Active }}{{ accent (symbol "Focus") }}{{ else }} {{ end }} ` +
			`{{ if .Selected }}{{ accent (symbol "Checked") }}{{ else }}{{ muted (symbol "Unchecked") }}{{ end }} {{ .Value }}`,
		AnswerValue: `{{ range $i, $v := .Values }}{{ if $i }}, {{ end }}{{ $v }}{{ else }}none{{ end }}`,
	},
	// FilePicker.Value names a [fileView]: directories end with a slash, the
	// way up is muted. It applies unless a theme sets Choice.Value.
	FilePicker: SelectTemplates{ChoiceTemplates: ChoiceTemplates{
		Value: `{{ if .Parent }}{{ muted .Name }}{{ else }}{{ .Name }}{{ if .IsDir }}/{{ end }}{{ end }}`,
	}},
	// Spinner rows name a [spinnerView]. Only a running spinner animates; a
	// stopped one is neutral, since closing it says nothing about success.
	Spinner: SpinnerTemplates{
		Running: `{{ accent .Frame }} {{ with .Prefix }}{{ text . }}{{ muted ":" }} {{ end }}{{ text .Message }}`,
		Stopped: `{{ muted (symbol "Cancel") }} {{ with .Prefix }}{{ text . }}{{ muted ":" }} {{ end }}{{ text .Message }}`,
		Failed:  `{{ error (symbol "Error") }} {{ with .Prefix }}{{ text . }}{{ muted ":" }} {{ end }}{{ text .Message }}`,
	},
	// Progress slots name a [progressView]. The renderer draws the bar in
	// the accent role and its empty track in the guide role.
	Progress: ProgressTemplates{
		Label:   `{{ text .Label }}`,
		Metrics: `{{ range $i, $v := .Values }}{{ if $i }}{{ muted " · " }}{{ end }}{{ muted $v }}{{ end }}`,
		Frame: `{{ with .Label }}{{ . }} {{ end }}{{ accent (printf "%3d%%" .Percent) }} ` +
			`{{ .Bar }}{{ with .Metrics }} {{ . }}{{ end }}`,
	},
	// Table slots name a [columnView] and a [cellView]. Weight sets the
	// header apart; values stay in the foreground unless a caller colors them.
	Table: TableTemplates{
		Header: `{{ bold (text .Header) }}`,
		Cell:   `{{ text .Value }}`,
	},
}

// compile resolves isolated slot inheritance over base and reports errors at defining slots.
func (t *Templates) compile(funcs template.FuncMap, base templates) (templates, error) {
	root := templated[struct{}, struct{}]{Funcs: funcs}
	var out templates
	var errs [12]error
	out.Prompt, errs[0] = t.prompt(root.nest("Prompt", t.Prompt, base.Prompt))
	out.Input, errs[1] = t.prompt(root.nest("Input", t.Input, out.Prompt))
	out.Password, errs[2] = t.secret(root.nest("Password", t.Password, out.Input))
	out.Number, errs[3] = t.secret(root.nest("Number", t.Number, out.Input))
	out.Choice, errs[4] = t.choice(root.nest("Choice", t.Choice, base.Choice))
	family := choiceFamily{out.Prompt, out.Choice}
	out.Dropdown, errs[5] = t.choiceFamily(root.nest("Dropdown", t.Dropdown, family))
	out.Multichoice, errs[6] = t.multichoice(root.nest("Multichoice", t.Multichoice, multichoiceTemplates{
		choiceFamily: family,
		Row:          base.Multichoice.Row,
		AnswerValue:  base.Multichoice.AnswerValue,
	}))
	out.Confirm, errs[7] = t.choiceFamily(root.nest("Confirm", t.Confirm, family))
	picker := family
	if t.Choice.Value == "" {
		// a shared projection reaches the picker, otherwise its own built-in
		picker.Value = base.FilePicker.Value
	}
	out.FilePicker, errs[8] = t.choiceFamily(root.nest("FilePicker", t.FilePicker, picker))
	out.Spinner, errs[9] = t.spinner(root.nest("Spinner", t.Spinner, base.Spinner))
	out.Progress, errs[10] = t.progress(root.nest("Progress", t.Progress, base.Progress))
	out.Table, errs[11] = t.table(root.nest("Table", t.Table, base.Table))
	if err := errors.Join(errs[:]...); err != nil {
		return templates{}, err
	}
	return out, nil
}

// PromptTemplates are the presentation slots shared by all prompts.
type PromptTemplates struct {
	Label    Template
	Answer   Template
	Hint     Template
	Feedback Template
}

// promptTemplates are the compiled [PromptTemplates].
type promptTemplates struct{ Label, Answer, Hint, Feedback *compiledSlot }

// prompt compiles prompt slots, inheriting unset slots from the parent.
func (t *Templates) prompt(in templated[PromptTemplates, promptTemplates]) (out promptTemplates, _ error) {
	var errs [4]error
	out.Label, errs[0] = t.slot(in.nest("Label", in.Src.Label, in.Parent.Label), true)
	out.Answer, errs[1] = t.slot(in.nest("Answer", in.Src.Answer, in.Parent.Answer), true)
	out.Hint, errs[2] = t.slot(in.nest("Hint", in.Src.Hint, in.Parent.Hint), true)
	out.Feedback, errs[3] = t.slot(in.nest("Feedback", in.Src.Feedback, in.Parent.Feedback), true)
	return out, errors.Join(errs[:]...)
}

// SecretTemplates are the slots of prompts that retain no answer line.
type SecretTemplates struct {
	Label    Template
	Hint     Template
	Feedback Template
}

// secretTemplates are the compiled [SecretTemplates].
type secretTemplates struct{ Label, Hint, Feedback *compiledSlot }

// secret compiles prompt slots without an answer slot, inheriting unset slots.
func (t *Templates) secret(in templated[SecretTemplates, promptTemplates]) (out secretTemplates, _ error) {
	var errs [3]error
	out.Label, errs[0] = t.slot(in.nest("Label", in.Src.Label, in.Parent.Label), true)
	out.Hint, errs[1] = t.slot(in.nest("Hint", in.Src.Hint, in.Parent.Hint), true)
	out.Feedback, errs[2] = t.slot(in.nest("Feedback", in.Src.Feedback, in.Parent.Feedback), true)
	return out, errors.Join(errs[:]...)
}

// ChoiceTemplates are the presentation slots shared by choice lists.
type ChoiceTemplates struct {
	Value    Template // projection of a raw item; replaced by WithTemplate
	Details  Template // active-only details
	Active   Template
	Inactive Template
	More     Template
	Empty    Template
}

// choiceTemplates are the compiled [ChoiceTemplates].
type choiceTemplates struct{ Value, Details, Active, Inactive, More, Empty *compiledSlot }

// choice compiles inherited choice slots, rejecting Hidden for Value, Active, and Inactive.
func (t *Templates) choice(in templated[ChoiceTemplates, choiceTemplates]) (out choiceTemplates, _ error) {
	var errs [6]error
	out.Value, errs[0] = t.slot(in.nest("Value", in.Src.Value, in.Parent.Value), false)
	out.Details, errs[1] = t.slot(in.nest("Details", in.Src.Details, in.Parent.Details), true)
	out.Active, errs[2] = t.slot(in.nest("Active", in.Src.Active, in.Parent.Active), false)
	out.Inactive, errs[3] = t.slot(in.nest("Inactive", in.Src.Inactive, in.Parent.Inactive), false)
	out.More, errs[4] = t.slot(in.nest("More", in.Src.More, in.Parent.More), true)
	out.Empty, errs[5] = t.slot(in.nest("Empty", in.Src.Empty, in.Parent.Empty), true)
	return out, errors.Join(errs[:]...)
}

// SelectTemplates specialize both the shared prompt and choice slots.
type SelectTemplates struct {
	PromptTemplates
	ChoiceTemplates
}

// choiceFamily are the compiled [SelectTemplates].
type choiceFamily struct {
	promptTemplates
	choiceTemplates
}

// choiceFamily compiles family overrides with shared prompt and choice fallbacks.
func (t *Templates) choiceFamily(in templated[SelectTemplates, choiceFamily]) (out choiceFamily, _ error) {
	var errs [2]error
	out.promptTemplates, errs[0] = t.prompt(in.nest("", in.Src.PromptTemplates, in.Parent.promptTemplates))
	out.choiceTemplates, errs[1] = t.choice(in.nest("", in.Src.ChoiceTemplates, in.Parent.choiceTemplates))
	return out, errors.Join(errs[:]...)
}

// MultichoiceTemplates add the row and collection body to a choice family.
type MultichoiceTemplates struct {
	SelectTemplates
	Row         Template
	AnswerValue Template
}

// multichoiceTemplates are the compiled [MultichoiceTemplates].
type multichoiceTemplates struct {
	choiceFamily
	Row, AnswerValue *compiledSlot
}

// multichoice compiles family slots with built-in fallbacks for Row and AnswerValue.
func (t *Templates) multichoice(
	in templated[MultichoiceTemplates, multichoiceTemplates],
) (out multichoiceTemplates, _ error) {
	var errs [3]error
	out.choiceFamily, errs[0] = t.choiceFamily(in.nest("", in.Src.SelectTemplates, in.Parent.choiceFamily))
	out.Row, errs[1] = t.slot(in.nest("Row", in.Src.Row, in.Parent.Row), false)
	out.AnswerValue, errs[2] = t.slot(in.nest("AnswerValue", in.Src.AnswerValue, in.Parent.AnswerValue), true)
	return out, errors.Join(errs[:]...)
}

// SpinnerTemplates are the rows of a spinner in each state of its lifecycle.
type SpinnerTemplates struct {
	Running Template
	Stopped Template // closed and kept with [WithKeep]; not a success
	Failed  Template
}

// spinnerTemplates are the compiled [SpinnerTemplates].
type spinnerTemplates struct{ Running, Stopped, Failed *compiledSlot }

// spinner compiles the spinner rows, which have no cross-family parent and
// cannot be hidden: a displayed spinner keeps its row.
func (t *Templates) spinner(in templated[SpinnerTemplates, spinnerTemplates]) (out spinnerTemplates, _ error) {
	var errs [3]error
	out.Running, errs[0] = t.slot(in.nest("Running", in.Src.Running, in.Parent.Running), false)
	out.Stopped, errs[1] = t.slot(in.nest("Stopped", in.Src.Stopped, in.Parent.Stopped), false)
	out.Failed, errs[2] = t.slot(in.nest("Failed", in.Src.Failed, in.Parent.Failed), false)
	return out, errors.Join(errs[:]...)
}

// ProgressTemplates are the row of a progress bar. Label and Metrics render
// first; Frame then places their output around the bar, which it must insert
// exactly once, as a top-level {{ .Bar }} that nothing else refers to, so
// that the bar can take the columns the rest of the row leaves.
type ProgressTemplates struct {
	Label   Template
	Metrics Template
	Frame   Template
}

// progressTemplates are the compiled [ProgressTemplates].
type progressTemplates struct{ Label, Metrics, Frame *compiledSlot }

// progress compiles the progress slots, which have no cross-family parent.
// Label and Metrics may be hidden; the frame holds the bar, so it cannot.
func (t *Templates) progress(in templated[ProgressTemplates, progressTemplates]) (out progressTemplates, _ error) {
	var errs [3]error
	out.Label, errs[0] = t.slot(in.nest("Label", in.Src.Label, in.Parent.Label), true)
	out.Metrics, errs[1] = t.slot(in.nest("Metrics", in.Src.Metrics, in.Parent.Metrics), true)
	out.Frame, errs[2] = t.slot(in.nest("Frame", in.Src.Frame, in.Parent.Frame), false)
	if errs[2] == nil && in.Src.Frame != "" {
		errs[2] = t.barOnce(out.Frame)
	}
	return out, errors.Join(errs[:]...)
}

// barOnce checks that a frame inserts the bar once, as a top-level action
// printing .Bar unchanged, and refers to .Bar nowhere else. Then nothing in
// the frame depends on the bar, and the renderer can measure the frame
// before sizing it.
func (t *Templates) barOnce(s *compiledSlot) error {
	inserts, uses := 0, 0
	if s.tmpl.Tree != nil {
		for _, n := range s.tmpl.Root.Nodes {
			if a, ok := n.(*parse.ActionNode); ok && t.isBar(a.Pipe) {
				inserts++
			}
		}
	}
	for _, tt := range s.tmpl.Templates() {
		if tt.Tree == nil {
			continue
		}
		err := t.walk(tt.Tree, tt.Root, func(_ *parse.Tree, n parse.Node) error {
			if t.usesBar(n) {
				uses++
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if inserts != 1 || uses != 1 {
		return fmt.Errorf("theme.Templates.%s: want one top-level {{ .Bar }} and no other use of .Bar", s.tmpl.Name())
	}
	return nil
}

// isBar reports whether a pipeline only prints .Bar.
func (*Templates) isBar(p *parse.PipeNode) bool {
	if len(p.Decl) > 0 || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	f, ok := p.Cmds[0].Args[0].(*parse.FieldNode)
	return ok && len(f.Ident) == 1 && f.Ident[0] == "Bar"
}

// usesBar reports whether n reads a Bar field: .Bar, $.Bar, $x.Bar or (x).Bar.
func (*Templates) usesBar(n parse.Node) bool {
	switch n := n.(type) {
	case *parse.FieldNode:
		return n.Ident[0] == "Bar"
	case *parse.VariableNode:
		return len(n.Ident) > 1 && n.Ident[1] == "Bar"
	case *parse.ChainNode:
		return n.Field[0] == "Bar"
	}
	return false
}

// TableTemplates are the header and the cells of tables, and the keys and
// values of facts. A hidden Header drops the header row, or the keys. Cell
// wraps each formatted value of an automatic row template, one physical line
// at a time, after any column template, formatter or scale; an explicit row
// template keeps its own presentation.
type TableTemplates struct {
	Header Template
	Cell   Template
}

// tableTemplates are the compiled [TableTemplates].
type tableTemplates struct{ Header, Cell *compiledSlot }

// table compiles the table slots, which have no cross-family parent. Only
// the header may be hidden: a cell keeps its value.
func (t *Templates) table(in templated[TableTemplates, tableTemplates]) (out tableTemplates, _ error) {
	var errs [2]error
	out.Header, errs[0] = t.slot(in.nest("Header", in.Src.Header, in.Parent.Header), true)
	out.Cell, errs[1] = t.slot(in.nest("Cell", in.Src.Cell, in.Parent.Cell), false)
	return out, errors.Join(errs[:]...)
}

// compiledSlot is one parsed presentation slot. A hidden slot renders nothing
// and reserves no space, and so does a nil slot, which has no built-in yet.
type compiledSlot struct {
	tmpl   *template.Template
	hidden bool
}

// slot parses an isolated source, inheriting unset sources and allowing Hidden only for optional slots.
func (t *Templates) slot(in templated[Template, *compiledSlot], optional bool) (*compiledSlot, error) {
	switch in.Src {
	case "":
		return in.Parent, nil
	case Hidden:
		if !optional {
			return nil, fmt.Errorf("theme.Templates.%s: empty source is not allowed", in.Name)
		}
		return &compiledSlot{hidden: true}, nil
	}
	tmpl, err := template.New(in.Name).Funcs(in.Funcs).Parse(in.Src.source())
	if err == nil {
		err = t.colorLiterals(tmpl)
	}
	if err != nil {
		return nil, fmt.Errorf("theme.Templates.%s: %w", in.Name, err)
	}
	return &compiledSlot{tmpl: tmpl}, nil
}

// colorLiterals checks the color argument of every fg and bg call that is a
// string literal, in every template of a slot, so that a typo fails the
// installation. Dynamic colors are checked when the template runs.
func (t *Templates) colorLiterals(tmpl *template.Template) error {
	for _, tt := range tmpl.Templates() {
		if tt.Tree == nil {
			continue
		}
		if err := t.walk(tt.Tree, tt.Root, t.colorLiteral); err != nil {
			return err
		}
	}
	return nil
}

// walk calls visit for n and every node below it in tree.
func (t *Templates) walk(tree *parse.Tree, n parse.Node, visit func(*parse.Tree, parse.Node) error) error {
	if err := visit(tree, n); err != nil {
		return err
	}
	var next []parse.Node
	switch n := n.(type) {
	case *parse.ListNode:
		if n != nil {
			next = n.Nodes
		}
	case *parse.ActionNode:
		next = []parse.Node{n.Pipe}
	case *parse.IfNode:
		next = []parse.Node{n.Pipe, n.List, n.ElseList}
	case *parse.RangeNode:
		next = []parse.Node{n.Pipe, n.List, n.ElseList}
	case *parse.WithNode:
		next = []parse.Node{n.Pipe, n.List, n.ElseList}
	case *parse.TemplateNode:
		next = []parse.Node{n.Pipe}
	case *parse.PipeNode:
		if n != nil {
			for _, c := range n.Cmds {
				next = append(next, c)
			}
		}
	case *parse.ChainNode:
		next = []parse.Node{n.Node}
	case *parse.CommandNode:
		next = n.Args
	}
	for _, c := range next {
		if err := t.walk(tree, c, visit); err != nil {
			return err
		}
	}
	return nil
}

// colorLiteral validates the color of an fg or bg command with a literal one.
func (*Templates) colorLiteral(tree *parse.Tree, node parse.Node) error {
	n, ok := node.(*parse.CommandNode)
	if !ok || len(n.Args) < 2 {
		return nil
	}
	id, isID := n.Args[0].(*parse.IdentifierNode)
	lit, isStr := n.Args[1].(*parse.StringNode)
	if !isID || !isStr || id.Ident != "fg" && id.Ident != "bg" {
		return nil
	}
	err := RGB(lit.Text).Validate()
	if lit.Text == "" {
		err = errors.New(`"": want "#RRGGBB" or "#RGB"`)
	}
	if err != nil {
		loc, _ := tree.ErrorContext(n)
		return fmt.Errorf("%s: %s: %w", loc, id.Ident, err)
	}
	return nil
}

// bind attaches destination helpers to a clone, preserving the installed slot.
func (s *compiledSlot) bind(funcs template.FuncMap) (boundSlot, error) {
	if s == nil || s.hidden {
		return boundSlot{}, nil
	}
	tmpl, err := s.tmpl.Clone()
	if err != nil {
		return boundSlot{}, fmt.Errorf("theme.Templates.%s: %w", s.tmpl.Name(), err)
	}
	return boundSlot{tmpl: tmpl.Funcs(funcs)}, nil
}

// boundSlot is a slot bound to one destination. The zero value is hidden.
type boundSlot struct {
	tmpl *template.Template
}

// render returns complete slot output or an error, allowing only text and SGR.
func (b boundSlot) render(data any) (text, error) {
	out, err := b.execute(data)
	if err == nil {
		err = b.styled(out)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// execute returns the slot output without checking it.
func (b boundSlot) execute(data any) (text, error) {
	if b.tmpl == nil {
		return nil, nil
	}
	var out text
	err := b.tmpl.Execute(&out, data)
	if err != nil {
		return nil, fmt.Errorf("theme.Templates.%s: %w", b.tmpl.Name(), err)
	}
	return out, nil
}

// styled reports slot output that is not only text and SGR.
func (b boundSlot) styled(out text) error {
	if seg, ok := out.unstyled(); ok {
		return fmt.Errorf("theme.Templates.%s: %q: want text and SGR only", b.tmpl.Name(), seg.text)
	}
	return nil
}

// WithFn registers or replaces a helper for operations started afterwards.
// Helpers must be safe for concurrent use and return a value or a value and an error.
// Invalid or reserved names and invalid signatures panic without changing the theme.
// Use [Theme.Funcs] with [SetDefaultTheme] to receive validation errors instead.
func WithFn(name string, fn any) {
	err := defaultIO.updateTheme(func(t *Theme) {
		if t.Funcs == nil {
			t.Funcs = template.FuncMap{}
		}
		t.Funcs[name] = fn
	})
	if err != nil {
		panic(fmt.Errorf("tui.WithFn: %w", err))
	}
}

// formatArgs joins helper arguments without separators.
func (span) formatArgs(args []any) text {
	var result text
	for _, t := range args {
		switch x := t.(type) {
		case *string:
			if x != nil {
				result = append(result, *x...)
			}
		case []string:
			result = append(result, strings.Join(x, ", ")...)
		case *int:
			if x != nil {
				result = fmt.Append(result, *x)
			}
		default:
			result = fmt.Append(result, t)
		}
	}
	return result
}

// span is the style one helper call establishes. It is immutable and keeps
// no state between calls, so its helpers are safe for concurrent executions.
type span struct {
	open     []sgrCode
	channels sgrChannel
	closer   string
}

// sgrCode is one opening SGR sequence and the channels it sets.
type sgrCode struct {
	seq      string
	channels sgrChannel
}

// helper adapts s to a template helper with the argument conversions of the attribute helpers.
func (s span) helper() func(...any) string {
	return func(args ...any) string { return s.wrap(s.formatArgs(args)).String() }
}

// wrap styles body, restoring outer channels after nested resets when content follows.
// Non-SGR escape sequences pass through unchanged.
func (s span) wrap(body text) text {
	if len(s.open) == 0 {
		return body
	}
	var out text
	for _, c := range s.open {
		out = append(out, c.seq...)
	}
	if bytes.IndexByte(body, 0x1b) < 0 {
		out = append(out, body...)
	} else {
		var pending sgrChannel
		for seg := range body.segments() {
			if pending != 0 {
				s.restore(&out, pending)
			}
			out = append(out, seg.text...)
			_, cleared := seg.sgrEffect()
			pending = cleared & s.channels
		}
	}
	out = append(out, s.closer...)
	return out
}

// restore appends the opening codes of s that set any of channels, in order.
func (s span) restore(out *text, channels sgrChannel) {
	for _, c := range s.open {
		if c.channels&channels != 0 {
			*out = append(*out, c.seq...)
		}
	}
}

// roleStyles are the encoded foregrounds of the semantic helpers and of the
// guide for one destination. An empty style keeps the terminal default.
type roleStyles struct {
	foreground, muted, guide, accent, success, failure text
}

// destination is what the built-in template helpers depend on: what one output
// accepts and shows.
type destination struct {
	profile colorProfile
	roles   roleStyles
	symbols Symbols
}

// newSpan defines a style wrapper that leaves text unchanged when no sequences are set.
func (d destination) newSpan(closer string, seqs ...text) span {
	s := span{closer: closer}
	for _, seq := range seqs {
		if len(seq) == 0 {
			continue
		}
		set, _ := seq.scan().sgrEffect()
		s.open = append(s.open, sgrCode{seq: seq.String(), channels: set})
		s.channels |= set
	}
	return s
}

// named returns the attribute helpers and ago.
func (d destination) named() template.FuncMap {
	var namedStyles = map[string]string{
		"bold":      bold,
		"dim":       dim,
		"italic":    italic,
		"underline": underline,
		"strike":    strikethrough,
	}
	funcs := make(template.FuncMap, len(namedStyles)+1)
	for name, seq := range namedStyles {
		funcs[name] = d.newSpan(reset, text(d.profile.named(text(seq)))).helper()
	}
	funcs["ago"] = ago
	return funcs
}

// funcs returns all built-in helpers bound to the destination.
func (d destination) funcs() template.FuncMap {
	funcs := d.named()
	for name, seq := range map[string]text{
		"text":    d.roles.foreground,
		"muted":   d.roles.muted,
		"accent":  d.roles.accent,
		"success": d.roles.success,
		"error":   d.roles.failure,
	} {
		funcs[name] = d.newSpan(fgDefault, seq).helper()
	}
	funcs["fg"] = d.color(false)
	funcs["bg"] = d.color(true)
	funcs["symbol"] = d.symbols.named
	funcs["label"] = choiceLabels{}.rowLabel
	// keycap names a key of a hint: the arrow pair takes the theme glyphs
	funcs["keycap"] = func(key string) string {
		if key == "up/down" {
			return d.symbols.Up + d.symbols.Down
		}
		return key
	}
	return funcs
}

// color returns a foreground or background helper that validates colors at execution.
func (d destination) color(background bool) func(string, ...any) (string, error) {
	name, closer := "fg", fgDefault
	if background {
		name, closer = "bg", bgDefault
	}
	return func(c string, args ...any) (string, error) {
		rgb, err := RGB(c).parse()
		if err == nil && c == "" {
			err = errors.New(`"": want "#RRGGBB" or "#RGB"`)
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		seq := d.profile.encode(rgb, background)
		s := d.newSpan(closer, seq)
		return s.wrap(s.formatArgs(args)).String(), nil
	}
}
