// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package option

// Default is implemented by widgets that accept a default answer or hint.
type Default interface {
	SetDefault(d any)
}

// AnswerHider is implemented by widgets that retain an answer after a question.
type AnswerHider interface {
	SetHideAnswer()
}

// Private capabilities keep existing option constructors shared by choice widgets.
type ChoiceContent interface {
	SetContentTemplate(main string, details ...string)
}
