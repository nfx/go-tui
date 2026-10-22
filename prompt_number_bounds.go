// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"math/big"
)

// numberBound is an inclusive limit, compared exactly.
type numberBound struct {
	rat  *big.Rat
	text string
}

type withNumberBounds interface {
	setMin(b *numberBound)
	setMax(b *numberBound)
}

// newNumberBound keeps a finite limit and its display text.
func newNumberBound[B numberType](v B) (*numberBound, error) {
	rat, text := numberRat(v)
	if rat == nil {
		return nil, fmt.Errorf("%w: bound must be finite", ErrInvalidOption)
	}
	return &numberBound{rat: rat, text: text}, nil
}
