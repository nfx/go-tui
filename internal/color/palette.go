// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package color

import (
	"sync"
)

var (
	// ANSIPalette returns the assumed RGB table: the 16 conventional entries,
	// the xterm 6x6x6 cube at 16+36r+6g+b, and the 24 grays at 232..255.
	// These are assumptions: terminals may redefine their palette.
	ANSIPalette = sync.OnceValue(func() (t [256]SRGB) {
		copy(t[:16], []SRGB{
			{0x00, 0x00, 0x00}, {0x80, 0x00, 0x00}, {0x00, 0x80, 0x00}, {0x80, 0x80, 0x00},
			{0x00, 0x00, 0x80}, {0x80, 0x00, 0x80}, {0x00, 0x80, 0x80}, {0xc0, 0xc0, 0xc0},
			{0x80, 0x80, 0x80}, {0xff, 0x00, 0x00}, {0x00, 0xff, 0x00}, {0xff, 0xff, 0x00},
			{0x00, 0x00, 0xff}, {0xff, 0x00, 0xff}, {0x00, 0xff, 0xff}, {0xff, 0xff, 0xff},
		})
		levels := [6]uint8{0, 95, 135, 175, 215, 255}
		for r := range 6 {
			for g := range 6 {
				for b := range 6 {
					t[16+36*r+6*g+b] = SRGB{levels[r], levels[g], levels[b]}
				}
			}
		}
		for k := range 24 {
			v := uint8(8 + 10*k)
			t[232+k] = SRGB{v, v, v}
		}
		return t
	})

	// ansiPaletteLab caches the OKLab value of every palette entry.
	ansiPaletteLab = sync.OnceValue(func() (t [256]oklab) {
		for i, c := range ANSIPalette() {
			t[i] = c.OKLab()
		}
		return t
	})
)
