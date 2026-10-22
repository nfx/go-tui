// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
)

// WithNonEmpty rejects blank input.
func WithNonEmpty() option.Opt {
	return option.OpT(func(i *input) error {
		i.rules = append(i.rules, func(_ context.Context, s string) error {
			if strings.TrimSpace(s) == "" {
				return ruleError("a value is required")
			}
			return nil
		})
		return nil
	})
}

// WithValidation validates confirmed input with fn.
func WithValidation(fn func(context.Context, string) error) option.Opt {
	return option.OpT(func(i *input) error {
		if fn == nil {
			return fmt.Errorf("%w: validator is nil", errs.ErrInvalidOption)
		}
		i.rules = append(i.rules, fn)
		return nil
	})
}

// WithRegex rejects input that does not fully match pattern.
func WithRegex(pattern, message string) option.Opt {
	return option.OpT(func(i *input) error {
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("%w: regex message is empty", errs.ErrInvalidOption)
		}
		// the non-capturing group keeps alternations like `a|b` from
		// escaping the \A and \z anchors.
		re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
		if err != nil {
			return fmt.Errorf("%w: regex: %w", errs.ErrInvalidOption, err)
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

func Input(ctx context.Context, label string, opts ...option.Opt) (string, error) {
	i := newInput(ctx, label)
	return i.read(ctx, opts...)
}

func Password(ctx context.Context, label string, opts ...option.Opt) (string, error) {
	i := newInput(ctx, label)
	i.Password = true
	return i.read(ctx, opts...)
}
