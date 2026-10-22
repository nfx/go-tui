// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"fmt"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/theme"
)

func WithOneReturn() option.Opt {
	return option.OpT(func(d *dropdown) error {
		d.OneReturn = true
		return nil
	})
}

// WithTemplate shares main content across choice rows and answers; details appear only on the active row.
func WithTemplate(main string, activeDetails ...string) option.Opt {
	return option.OpT(func(d option.ChoiceContent) error {
		d.SetContentTemplate(main, activeDetails...)
		return nil
	})
}

// SetContentTemplate keeps active-only details out of inactive rows and answers.
func (d *dropdown) SetContentTemplate(main string, activeDetails ...string) {
	d.setProjection(main, activeDetails...)
}

// parseTemplates parses the projection for later destination binding.
func (d *dropdown) parseTemplates() error {
	return d.parse(d.theme, "dropdown")
}

// dropdownKeys are the bindings beyond confirming that the hint row describes.
func (d *dropdown) dropdownKeys(s theme.Symbols) []render.Hint {
	return []render.Hint{
		{Left: s.Arrows(), Right: ansi.Text("move")},
		{Left: ansi.Text("type"), Right: ansi.Text("filter")},
	}
}

type oneHatch int //nolint:errname // hack for [WithDefault]

func (i oneHatch) Error() string {
	return fmt.Sprintf("%d", i)
}
