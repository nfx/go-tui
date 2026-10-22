// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package option

import (
	"io"
)

type IO interface {
	SetWriter(w io.Writer)
	SetReader(r io.Reader)
}

// Default is implemented by widgets that accept a default answer or hint.
type Default interface {
	SetDefault(d any)
}

// Prompt is implemented by widgets that ask a question and print the answer.
type Prompt interface {
	SetHide()
	Default
}

// Private capabilities keep existing option constructors shared by choice widgets.
type ChoiceContent interface {
	SetContentTemplate(main string, details ...string)
}
