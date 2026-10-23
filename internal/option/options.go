// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package option

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
