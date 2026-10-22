// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"io"
)

// WithOptions bundles options for reuse across widgets. Each member is applied
// when the widget supports it and skipped otherwise. The bundle fails with
// [ErrWrongWidget] only when the widget supports none of its members. Any
// other error from a member, such as an invalid value, is returned as is.
func WithOptions(o ...Opt) Opt {
	return func(target any) error {
		if len(o) == 0 {
			return nil
		}
		applied := false
		for _, o := range o {
			err := o(target)
			if errors.Is(err, ErrWrongWidget) {
				continue
			}
			if err != nil {
				return err
			}
			applied = true
		}
		if !applied {
			return fmt.Errorf("%w: no option in the bundle supports %T", ErrWrongWidget, target)
		}
		return nil
	}
}

// WithInput overrides the input source of an interactive widget.
func WithInput(r io.Reader) Opt {
	return OpT(func(x IO) error {
		x.SetReader(r)
		return nil
	})
}

// WithOutput overrides the output destination of an interactive widget.
func WithOutput(w io.Writer) Opt {
	return OpT(func(x IO) error {
		x.SetWriter(w)
		return nil
	})
}
