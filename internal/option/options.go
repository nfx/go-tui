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

// WithHideAnswer skips retaining the answer of a widget that asks a question.
// It cannot show an answer that the theme hides.
func WithHideAnswer() Opt {
	return OpT(func(x AnswerHider) error {
		x.SetHideAnswer()
		return nil
	})
}
