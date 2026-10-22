// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"math/big"
	"strings"
)

// decimalDraft represents coef × 10^-scale.
type decimalDraft struct {
	coef  *big.Int
	scale int
	// dot preserves a trailing decimal point.
	dot bool
}

// pow10 supplies exact decimal scale factors.
func (d decimalDraft) pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// step adds or subtracts 10^exp exactly.
func (d decimalDraft) step(exp int, up bool) decimalDraft {
	scale := max(d.scale, -exp)
	coef := new(big.Int).Mul(d.coef, d.pow10(scale-d.scale))
	delta := d.pow10(scale + exp)
	if up {
		coef.Add(coef, delta)
	} else {
		coef.Sub(coef, delta)
	}
	return decimalDraft{coef: coef, scale: scale, dot: d.dot}
}

// String formats the value at its original scale.
func (d decimalDraft) String() string {
	digits := new(big.Int).Abs(d.coef).String()
	if len(digits) <= d.scale {
		digits = strings.Repeat("0", d.scale-len(digits)+1) + digits
	}
	whole, frac := digits[:len(digits)-d.scale], digits[len(digits)-d.scale:]
	s := whole
	if d.scale > 0 {
		s += "." + frac
	} else if d.dot {
		s += "."
	}
	if d.coef.Sign() < 0 {
		s = "-" + s
	}
	return s
}

type caretKind int

const (
	caretDigit caretKind = iota // before a digit, which selects its place
	caretEnd
	caretDot
	caretSign
)
