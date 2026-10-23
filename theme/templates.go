// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

// Template is an optional template source. The zero value inherits from the
// parent slot; [Hidden] is an explicit empty source that hides the slot
// instead of falling back to a parent.
type Template string

// Hidden deliberately hides a slot. It is a sentinel, not template text.
const Hidden Template = "\x00hidden"

// Prompt holds the slots shared by all prompts. An unset slot of Input,
// Dropdown and the other widgets inherits from here, and finally from the
// built-in default, so a shared edit still reaches every widget that has not
// overridden it.
type Prompt struct {
	// Label is the question row.
	Label Template
	// Answer is the line kept after the prompt is answered.
	Answer Template
	// Hint is the row of key hints.
	Hint Template
	// Feedback is the validation error row.
	Feedback Template
}

// Choice holds the slots shared by choice lists.
type Choice struct {
	// Value projects a raw item to its text; replaced by WithTemplate.
	Value Template
	// Details is the extra text of the active item.
	Details Template
	// Active is the row of the active item.
	Active Template
	// Inactive is the row of any other item.
	Inactive Template
	// Empty is the row shown when no item matches.
	Empty Template
}

// Input holds the slots of text input prompts, inheriting from [Prompt].
type Input Prompt

// Password holds the slots of password prompts, inheriting from [Input]. A
// password retains no answer line, so it has no Answer slot.
type Password struct {
	// Label is the question row.
	Label Template
	// Hint is the row of key hints.
	Hint Template
	// Feedback is the validation error row.
	Feedback Template
}

// Number holds the slots of number prompts, inheriting from [Input]. Its
// answer line is [Input.Answer].
type Number Password

// Dropdown specializes both the shared [Prompt] and [Choice] slots.
type Dropdown struct {
	Prompt
	Choice
}

// Confirm specializes both the shared [Prompt] and [Choice] slots.
type Confirm Dropdown

// FilePicker specializes both the shared [Prompt] and [Choice] slots. Its
// Value is the file projection, which a shared [Choice.Value] replaces.
type FilePicker Dropdown

// Multichoice specializes the shared [Prompt] and [Choice] slots, and adds the
// row and the collection body.
type Multichoice struct {
	Prompt
	Choice
	// Row is the row of one item with its checkbox.
	Row Template
	// AnswerValue is the collection of selected values in the answer.
	AnswerValue Template
}
