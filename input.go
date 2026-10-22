// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
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
func WithDefault(d any) opt {
	return opT(func(x withDefault) error {
		x.setDefault(d)
		return nil
	})
}

// WithNonEmpty rejects confirmed input containing only whitespace or no characters.
func WithNonEmpty() opt {
	return opT(func(i *input) error {
		i.rules = append(i.rules, func(_ context.Context, s string) error {
			if strings.TrimSpace(s) == "" {
				return ruleError("a value is required")
			}
			return nil
		})
		return nil
	})
}

// WithValidation validates confirmed input in option order; the first error is shown
// below the input. fn receives the prompt context and blocks key handling while it
// runs. Errors must not expose the typed value, especially for [Password].
func WithValidation(fn func(context.Context, string) error) opt {
	return opT(func(i *input) error {
		if fn == nil {
			return fmt.Errorf("%w: validator is nil", ErrInvalidOption)
		}
		i.rules = append(i.rules, fn)
		return nil
	})
}

// WithRegex rejects confirmed input that does not fully match pattern, showing message.
// A pattern that matches the empty string accepts it;
// combine with [WithNonEmpty] to require a value.
func WithRegex(pattern, message string) opt {
	return opT(func(i *input) error {
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("%w: regex message is empty", ErrInvalidOption)
		}
		// the non-capturing group keeps alternations like `a|b` from
		// escaping the \A and \z anchors.
		re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
		if err != nil {
			return fmt.Errorf("%w: regex: %w", ErrInvalidOption, err)
		}
		i.rules = append(i.rules, func(_ context.Context, s string) error {
			if !re.MatchString(s) {
				return ruleError(message)
			}
			return nil
		})
		return nil
	})
}

// ruleError rejects input for a built-in rule. Its message comes from the
// option, never from the typed text, so a [Password] may show it.
type ruleError string

// Error returns the built-in rule's rejection message.
func (e ruleError) Error() string { return string(e) }

func Input(ctx context.Context, label string, option ...opt) (string, error) {
	i := newInput(ctx, label)
	return i.read(ctx, option...)
}

func Password(ctx context.Context, label string, option ...opt) (string, error) {
	i := newInput(ctx, label)
	i.Password = true
	return i.read(ctx, option...)
}
