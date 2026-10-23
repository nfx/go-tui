// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package option

import (
	"errors"
	"fmt"

	"github.com/nfx/go-tui/internal/errs"
)

// WithOptions bundles options: each member applies where the widget supports
// it, and the bundle fails only when the widget supports none of them.
func WithOptions(o ...Opt) Opt {
	return func(target any) error {
		if len(o) == 0 {
			return nil
		}
		applied := false
		for _, o := range o {
			err := o(target)
			if errors.Is(err, errs.ErrWrongWidget) {
				continue
			}
			if err != nil {
				return err
			}
			applied = true
		}
		if !applied {
			return fmt.Errorf("%w: no option in the bundle supports %T", errs.ErrWrongWidget, target)
		}
		return nil
	}
}

// WithDefault sets the default answer of a widget that accepts one.
func WithDefault(d any) Opt {
	return OpT(func(x Default) error {
		x.SetDefault(d)
		return nil
	})
}

// WithHide skips printing the answer of a widget that asks a question.
func WithHide() Opt {
	return OpT(func(x Prompt) error {
		x.SetHide()
		return nil
	})
}
