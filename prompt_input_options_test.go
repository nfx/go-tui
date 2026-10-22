// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestInputWithDefault(t *testing.T) {
	i := newInput(t.Context(), "label")
	err := WithDefault("abc")(i)
	assert.NoError(t, err)
	assert.Equal(t, "abc", i.typed)
	assert.Equal(t, 3, i.cursor)
}

func TestInputWithDefaultCountsRunes(t *testing.T) {
	i := newInput(t.Context(), "label")
	err := WithDefault("héllo")(i)
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
func runValidated(t *testing.T, password bool, keys string, o ...Opt) (string, string, error) {
	t.Helper()
	i := newInput(t.Context(), "Label")
	i.Password = password
	var chunks [][]byte
	for _, k := range keys {
		chunks = append(chunks, []byte(string(k)))
	}
	i.in = &chunkReader{chunks: chunks}
	var out bytes.Buffer
	i.out = &out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 40, Height: 3, Restore: func() error { return nil }}, nil
	}
	if err := Opts(o).Apply(i); err != nil {
		return "", "", err
	}
	got, err := i.run(i.ctx)
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
	assert.ErrorIs(t, err, ErrInvalidOption)
}

func TestInputValidatorWrongWidget(t *testing.T) {
	err := WithValidation(func(context.Context, string) error { return nil })(newDropdown(t.Context()))
	assert.ErrorIs(t, err, ErrWrongWidget)
}

func TestInputValidatesDefault(t *testing.T) {
	_, out, err := runValidated(t, false, "\r", WithDefault("bad"), WithRegex(`[0-9]+`, "digits only"))
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "✖ ")
	assert.Contains(t, out, "digits only")
	got, _, err := runValidated(t, false, "\r", WithDefault("42"), WithRegex(`[0-9]+`, "digits only"))
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
	assert.ErrorIs(t, WithRegex(`(`, "m")(newInput(t.Context(), "x")), ErrInvalidOption)
	assert.ErrorIs(t, WithRegex(`a`, "  ")(newInput(t.Context(), "x")), ErrInvalidOption)
}

func TestInputDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()

	got, err := Input(ctx, "Name", io, WithDefault("bob"))
	assert.NoError(t, err)
	assert.Equal(t, "bob", got)

	got, err = Input(ctx, "Name", io, WithDefault(""))
	assert.NoError(t, err)
	assert.Equal(t, "", got)

	got, err = Input(ctx, "Name", io, WithDefault(0))
	assert.NoError(t, err)
	assert.Equal(t, "0", got)

	got, err = Input(ctx, "Name", io, WithDefault(nil))
	assert.NoError(t, err)
	assert.Equal(t, "<nil>", got)

	got, err = Input(ctx, "Name", io, WithDefault("first"), WithDefault("last"))
	assert.NoError(t, err)
	assert.Equal(t, "last", got)

	_, err = Input(ctx, "Name", io)
	assert.ErrorIs(t, err, ErrNoTTY)
}

func TestInputDefaultValidation(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()

	_, err := Input(ctx, "Name", io, WithNonEmpty(), WithDefault(""))
	assert.ErrorIs(t, err, ErrInvalidOption)

	cause := errors.New("nope")
	_, err = Input(ctx, "Name", io, WithDefault("x"),
		WithValidation(func(context.Context, string) error { return cause }))
	assert.ErrorIs(t, err, ErrInvalidOption)
	assert.ErrorIs(t, err, cause)

	_, err = Input(ctx, "Name", io, WithDefault("abc"), WithRegex(`\d+`, "digits"))
	assert.ErrorIs(t, err, ErrInvalidOption)

	got, err := Input(ctx, "Name", io, WithDefault("123"), WithRegex(`\d+`, "digits"))
	assert.NoError(t, err)
	assert.Equal(t, "123", got)

	cctx, cancel := context.WithCancel(ctx)
	_, err = Input(cctx, "Name", io, WithDefault("x"),
		WithValidation(func(context.Context, string) error { cancel(); return nil }))
	assert.ErrorIs(t, err, context.Canceled)
}
