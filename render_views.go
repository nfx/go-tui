// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"github.com/nfx/go-tui/internal/ansi"
)

// labelView is the payload of a label slot.
type labelView struct{ Label string }

// answerView is the payload of an answer slot: Value is the formatted answer,
// Values and Count the answer as a collection, for scalar prompts one value.
type answerView struct {
	Label  string
	Value  string
	Values []string
	Count  int
}

// feedbackView is the payload of a feedback slot: the sanitized rejection message.
type feedbackView struct{ Message string }

// hint is one entry of a hint row: a key and what it does, a count and
// what it counts, or a value and its unit.
type hint struct{ Left, Right ansi.Text }

// hintView is the payload of a hint slot: the keys the widget actually
// handles, led by the count of hidden items in a choice list, or the
// informational metrics of a progress bar.
type hintView []hint
