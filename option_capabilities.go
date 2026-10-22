// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"io"
)

type withIO interface {
	setWriter(w io.Writer)
	setReader(r io.Reader)
}

// withDefault is implemented by widgets that accept a default answer or hint.
type withDefault interface {
	setDefault(d any)
}

// withPrompt is implemented by widgets that ask a question and print the answer.
type withPrompt interface {
	setHide()
	withDefault
}

// Private capabilities keep existing option constructors shared by choice widgets.
type withChoiceContent interface {
	setContentTemplate(main string, details ...string)
}
