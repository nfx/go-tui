// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"io"

	"github.com/nfx/go-tui/internal/option"
)

// WithOptions bundles options for reuse across widgets. Each member is applied
// when the widget supports it and skipped otherwise. The bundle fails with
// [ErrWrongWidget] only when the widget supports none of its members. Any
// other error from a member, such as an invalid value, is returned as is.
func WithOptions(o ...option.Opt) option.Opt { return option.WithOptions(o...) }

// WithInput overrides the input source of an interactive widget.
func WithInput(r io.Reader) option.Opt { return option.WithInput(r) }

// WithOutput overrides the output destination of an interactive widget.
func WithOutput(w io.Writer) option.Opt { return option.WithOutput(w) }
