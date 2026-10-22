// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"github.com/nfx/go-tui/internal/ansi"
)

// LabelView is the payload of a label slot.
type LabelView struct{ Label string }

// AnswerView is the payload of an answer slot: Value is the formatted answer,
// Values and Count the answer as a collection, for scalar prompts one value.
type AnswerView struct {
	Label  string
	Value  string
	Values []string
	Count  int
}

// FeedbackView is the payload of a feedback slot: the sanitized rejection message.
type FeedbackView struct{ Message string }

// Hint is one entry of a Hint row: a key and what it does, a count and
// what it counts, or a value and its unit.
type Hint struct{ Left, Right ansi.Text }

// HintView is the payload of a hint slot: the keys the widget actually
// handles, led by the count of hidden items in a choice list, or the
// informational metrics of a progress bar.
type HintView []Hint
