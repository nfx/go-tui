// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"

	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/prompt"
)

// WithDefault supplies the answer of a prompt. Without a terminal the prompt
// returns it without reading input or printing; with a default missing, invalid
// or ambiguous it fails with [ErrInvalidOption], and without any default with
// [ErrNoTTY]. A default of nil, "", 0 or false is still a default.
//
// [Input] and [Password] pre-fill fmt.Sprint(d). [Number] pre-fills d in plain
// decimal notation. Choice widgets keep d as a typed value: the item for
// [Dropdown], the key for [DropdownKV] and [MultichoiceKVK], the value for
// [MultichoiceKVV]; a multichoice also takes a slice of them. Values are
// matched by equality, never by label, and must identify exactly one entry. A
// string that is not an item is a word-prefix hint on the labels.
//
// With a terminal, a dropdown treats a string as a match hint, not a pre-filled
// filter: items are ranked by similarity to d, and the dropdown returns early
// only when exactly one item matches d as a prefix. Multichoice checks a unique
// word-prefix match once and still requires Enter. Lazy choices resolve the
// default after successful exhaustion. [Confirm] takes a bool or "Yes"/"No".
func WithDefault(d any) option.Opt { return option.WithDefault(d) }

// WithNonEmpty rejects confirmed input containing only whitespace or no characters.
func WithNonEmpty() option.Opt { return prompt.WithNonEmpty() }

// WithValidation validates confirmed input in option order; the first error is shown
// below the input. fn receives the prompt context and blocks key handling while it
// runs. Errors must not expose the typed value, especially for [Password].
func WithValidation(fn func(context.Context, string) error) option.Opt {
	return prompt.WithValidation(fn)
}

// WithRegex rejects confirmed input that does not fully match pattern, showing message.
// A pattern that matches the empty string accepts it;
// combine with [WithNonEmpty] to require a value.
func WithRegex(pattern, message string) option.Opt { return prompt.WithRegex(pattern, message) }

func Input(ctx context.Context, label string, opts ...option.Opt) (string, error) {
	return prompt.Input(ctx, label, opts...)
}

func Password(ctx context.Context, label string, opts ...option.Opt) (string, error) {
	return prompt.Password(ctx, label, opts...)
}
