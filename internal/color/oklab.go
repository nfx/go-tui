// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package color

import (
	"math"
)

// OKLab converts to OKLab using Björn Ottosson's matrices.
// see https://bottosson.github.io/posts/OKLab/#converting-from-linear-srgb-to-OKLab.
func (c SRGB) OKLab() oklab {
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

// Mix moves from c toward o by t in 0..1.
func (c oklab) Mix(o oklab, t float64) SRGB {
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
func (c oklab) srgb() (rgb SRGB, inGamut bool) {
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
func (c oklch) srgb() SRGB {
	at := func(chroma float64) (SRGB, bool) {
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
func (c oklch) withLightness(l float64) SRGB {
	c.L = l
	return c.srgb()
}
