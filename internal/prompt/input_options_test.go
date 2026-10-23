// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestInputWithDefault(t *testing.T) {
	i := newInput(t.Context(), "label")
	err := option.WithDefault("abc")(i)
	assert.NoError(t, err)
	assert.Equal(t, "abc", i.typed)
	assert.Equal(t, 3, i.cursor)
}

func TestInputWithDefaultCountsRunes(t *testing.T) {
	i := newInput(t.Context(), "label")
	err := option.WithDefault("héllo")(i)
	assert.NoError(t, err)
	assert.Equal(t, 5, i.cursor)
}

func TestInputWithNonEmpty(t *testing.T) {
	i := newInput(t.Context(), "label")
	err := WithNonEmpty()(i)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(i.rules))
}

// runValidated drives validation with scripted keys and captures output on a 40x3 terminal.
func runValidated(t *testing.T, password bool, keys string, o ...option.Opt) (string, string, error) {
	t.Helper()
	i := newInput(t.Context(), "Label")
	i.Password = password
	in := terst.NewKeys(t)
	for _, k := range keys {
		in.Send(string(k))
	}
	in.Close() // input ends after the script
	i.In = in
	var out bytes.Buffer
	i.Out = &out
	i.makeTermIO = terst.FixedTermIO(40, 3)
	if err := option.Opts(o).Apply(i); err != nil {
		return "", "", err
	}
	got, err := i.run(i.Ctx)
	return got, out.String(), err
}

func TestInputValidatorRejectsThenAccepts(t *testing.T) {
	var seen []string
	check := WithValidation(func(_ context.Context, s string) error {
		seen = append(seen, s)
		if len(s) < 2 {
			return errors.New("too short")
		}
		return nil
	})
	got, out, err := runValidated(t, false, "a\rb\r", check)
	assert.NoError(t, err)
	assert.Equal(t, "ab", got)
	assert.Equal(t, []string{"a", "ab"}, seen)
	assert.Contains(t, out, "✖ ")
	assert.Contains(t, out, "too short")
	assert.Contains(t, out, "\x1b[1A")
}

func TestInputValidatorEOFAfterRejectionReturnsNoValue(t *testing.T) {
	reject := WithValidation(func(context.Context, string) error { return errors.New("no") })
	got, _, err := runValidated(t, false, "a\r", reject)
	assert.Equal(t, "", got)
	assert.ErrorIs(t, err, io.EOF)
}

func TestInputValidatorsRunInOrderAndStopAtFirstFailure(t *testing.T) {
	var calls []string
	first := WithValidation(
		func(context.Context, string) error { calls = append(calls, "first"); return errors.New("one") },
	)
	second := WithValidation(
		func(context.Context, string) error { calls = append(calls, "second"); return errors.New("two") },
	)
	_, out, err := runValidated(t, false, "a\r", first, second)
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, []string{"first"}, calls)
	assert.Contains(t, out, "one")
	assert.True(t, !strings.Contains(out, "two"))
}

func TestInputValidatorEmptyMessageFallsBack(t *testing.T) {
	blank := WithValidation(func(context.Context, string) error { return errors.New("") })
	_, out, err := runValidated(t, false, "a\r", blank)
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "Invalid value")
}

func TestInputValidatorNilIsInvalidOption(t *testing.T) {
	err := WithValidation(nil)(newInput(t.Context(), "x"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
}

func TestInputValidatorWrongWidget(t *testing.T) {
	err := WithValidation(func(context.Context, string) error { return nil })(&struct{}{})
	assert.ErrorIs(t, err, errs.ErrWrongWidget)
}

func TestInputValidatesDefault(t *testing.T) {
	_, out, err := runValidated(t, false, "\r", option.WithDefault("bad"), WithRegex(`[0-9]+`, "digits only"))
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "✖ ")
	assert.Contains(t, out, "digits only")
	got, _, err := runValidated(t, false, "\r", option.WithDefault("42"), WithRegex(`[0-9]+`, "digits only"))
	assert.NoError(t, err)
	assert.Equal(t, "42", got)
}

func TestInputNonEmptyRejectsBlank(t *testing.T) {
	got, out, err := runValidated(t, false, " \r\x7fa\r", WithNonEmpty())
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
	assert.Contains(t, out, "a value is required")
}

func TestInputRegexMatchesWholeString(t *testing.T) {
	re := WithRegex(`a|b`, "a or b")
	got, _, err := runValidated(t, false, "ab\x7fb\x7f\x7fa\r", re)
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
	_, out, err := runValidated(t, false, "ab\r", re)
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "a or b")
	// matches empty without a separate non-empty rule
	got, _, err = runValidated(t, false, "\r", WithRegex(`a*`, "a's"))
	assert.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestInputRegexMalformedOrBlankMessage(t *testing.T) {
	assert.ErrorIs(t, WithRegex(`(`, "m")(newInput(t.Context(), "x")), errs.ErrInvalidOption)
	assert.ErrorIs(t, WithRegex(`a`, "  ")(newInput(t.Context(), "x")), errs.ErrInvalidOption)
}

func TestInputDefaults(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()

	got, err := Input(ctx, "Name", option.WithDefault("bob"))
	assert.NoError(t, err)
	assert.Equal(t, "bob", got)

	got, err = Input(ctx, "Name", option.WithDefault(""))
	assert.NoError(t, err)
	assert.Equal(t, "", got)

	got, err = Input(ctx, "Name", option.WithDefault(0))
	assert.NoError(t, err)
	assert.Equal(t, "0", got)

	got, err = Input(ctx, "Name", option.WithDefault(nil))
	assert.NoError(t, err)
	assert.Equal(t, "<nil>", got)

	got, err = Input(ctx, "Name", option.WithDefault("first"), option.WithDefault("last"))
	assert.NoError(t, err)
	assert.Equal(t, "last", got)

	_, err = Input(ctx, "Name")
	assert.ErrorIs(t, err, errs.ErrNoTTY)
}

func TestInputDefaultValidation(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()

	_, err := Input(ctx, "Name", WithNonEmpty(), option.WithDefault(""))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	cause := errors.New("nope")
	_, err = Input(ctx, "Name", option.WithDefault("x"),
		WithValidation(func(context.Context, string) error { return cause }))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	assert.ErrorIs(t, err, cause)

	_, err = Input(ctx, "Name", option.WithDefault("abc"), WithRegex(`\d+`, "digits"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	got, err := Input(ctx, "Name", option.WithDefault("123"), WithRegex(`\d+`, "digits"))
	assert.NoError(t, err)
	assert.Equal(t, "123", got)

	cctx, cancel := context.WithCancel(ctx)
	_, err = Input(cctx, "Name", option.WithDefault("x"),
		WithValidation(func(context.Context, string) error { cancel(); return nil }))
	assert.ErrorIs(t, err, context.Canceled)
}
