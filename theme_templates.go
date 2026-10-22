// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

// Template is an optional template source. The zero value inherits from the
// parent slot; [Hidden] is an explicit empty source that hides the slot
// instead of falling back to a parent.
type Template string

// Hidden deliberately hides a slot. It is a sentinel, not template text.
const Hidden Template = "\x00hidden"

// source returns the template text; it is empty for [Hidden].
func (t Template) source() string {
	if t == Hidden {
		return ""
	}
	return string(t)
}

// Templates are the typed presentation slots of a theme. An unset slot
// inherits from its parent family and finally from the built-in default, so a
// shared edit still reaches every family that has not overridden it.
type Templates struct {
	Prompt PromptTemplates
	Choice ChoiceTemplates

	Input    PromptTemplates
	Password SecretTemplates
	Number   SecretTemplates

	Dropdown    SelectTemplates
	Multichoice MultichoiceTemplates
	Confirm     SelectTemplates
	FilePicker  SelectTemplates
	Spinner     SpinnerTemplates
	Progress    ProgressTemplates
	Table       TableTemplates
}

// PromptTemplates are the presentation slots shared by all prompts.
type PromptTemplates struct {
	Label    Template
	Answer   Template
	Hint     Template
	Feedback Template
}

// SecretTemplates are the slots of prompts that retain no answer line.
type SecretTemplates struct {
	Label    Template
	Hint     Template
	Feedback Template
}

// ChoiceTemplates are the presentation slots shared by choice lists.
type ChoiceTemplates struct {
	Value    Template // projection of a raw item; replaced by WithTemplate
	Details  Template // active-only details
	Active   Template
	Inactive Template
	Empty    Template
}

// SelectTemplates specialize both the shared prompt and choice slots.
type SelectTemplates struct {
	PromptTemplates
	ChoiceTemplates
}

// MultichoiceTemplates add the row and collection body to a choice family.
type MultichoiceTemplates struct {
	SelectTemplates
	Row         Template
	AnswerValue Template
}

// SpinnerTemplates are the rows of a spinner in each state of its lifecycle.
type SpinnerTemplates struct {
	Running Template
	Stopped Template // closed and kept with [WithKeep]; not a success
	Failed  Template
}

// ProgressTemplates are the rows of a progress bar. Label and Metrics render
// first; Frame then places their output around the bar, which it must insert
// exactly once, as a top-level {{ .Bar }} that nothing else refers to, so
// that the bar can take the columns the rest of the row leaves.
//
// Metrics is the end of the bar row of [ProgressStyle.Compact] bars. Other
// bars show the metrics in the Hint row below the bar instead, and their
// Frame receives an empty .Metrics. Hint renders the [hint] pairs of the metrics,
// each with .Left and .Right, such as "6.2" and "MiB/s".
type ProgressTemplates struct {
	Label   Template
	Metrics Template
	Hint    Template
	Frame   Template
}

// TableTemplates are the header and the cells of tables, and the keys and
// values of facts. A hidden Header drops the header row, or the keys. Cell
// wraps each formatted value of an automatic row template, one physical line
// at a time, after any column template, formatter or scale; an explicit row
// template keeps its own presentation.
type TableTemplates struct {
	Header Template
	Cell   Template
}
