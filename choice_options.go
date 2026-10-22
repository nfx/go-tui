// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
)

func WithOneReturn() opt {
	return opT(func(d *dropdown) error {
		d.OneReturn = true
		return nil
	})
}

// WithTemplate shares main content across choice rows and answers; details appear only on the active row.
func WithTemplate(main string, activeDetails ...string) opt {
	return opT(func(d withChoiceContent) error {
		d.setContentTemplate(main, activeDetails...)
		return nil
	})
}

// setContentTemplate keeps active-only details out of inactive rows and answers.
func (d *dropdown) setContentTemplate(main string, activeDetails ...string) {
	d.setProjection(main, activeDetails...)
}

// parseTemplates parses the projection for later destination binding.
func (d *dropdown) parseTemplates() error {
	return d.parse(d.theme, "dropdown")
}

// dropdownKeys are the bindings beyond confirming that the hint row describes.
func (d *dropdown) dropdownKeys(s Symbols) []hint {
	return []hint{{s.arrows(), ansi.Text("move")}, {ansi.Text("type"), ansi.Text("filter")}}
}

type oneHatch int //nolint:errname // hack for [WithDefault]

func (i oneHatch) Error() string {
	return fmt.Sprintf("%d", i)
}
