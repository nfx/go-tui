// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"fmt"
	"math/big"

	"github.com/nfx/go-tui/internal/errs"
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
func newNumberBound[B Numeric](v B) (*numberBound, error) {
	rat, text := numberRat(v)
	if rat == nil {
		return nil, fmt.Errorf("%w: bound must be finite", errs.ErrInvalidOption)
	}
	return &numberBound{rat: rat, text: text}, nil
}
