// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/tcommon"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/terst"
)

func TestPasswordReadsFromPipe(t *testing.T) {
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	_, err = w.WriteString("secret\r")
	assert.NoError(t, err)
	assert.NoError(t, w.Close())
	terst.UseTerminalInput(t, &tcommon.FDByteReader{F: r})
	got, err := Password(t.Context(), "Label")
	assert.NoError(t, err)
	assert.Equal(t, "secret", got)
}

func TestPasswordRejectionStaysMasked(t *testing.T) {
	_, out, err := runValidated(
		t,
		true,
		"secret\r",
		WithValidation(func(context.Context, string) error { return errors.New("too weak") }),
	)
	assert.ErrorIs(t, err, io.EOF)
	// a custom message may repeat the secret, so a password shows a fixed one
	assert.Contains(t, out, "✖ Invalid value")
	assert.True(t, !strings.Contains(out, "too weak"))
	assert.True(t, !strings.Contains(out, "secret"))
}

func TestPasswordDefaultKeepsSecretOut(t *testing.T) {
	terst.Quiet(t)
	got, err := Password(t.Context(), "Secret", option.WithDefault("hunter2"))
	assert.NoError(t, err)
	assert.Equal(t, "hunter2", got)

	_, err = Password(t.Context(), "Secret", option.WithDefault("hunter2"),
		WithValidation(func(context.Context, string) error { return errors.New("too weak") }))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	assert.True(t, !strings.Contains(err.Error(), "hunter2"))
}

// themedPassword prepares a themed password prompt with simulated keys and captured output.
func themedPassword(t *testing.T, th theme.Theme, keys ...string) (*input, *bytes.Buffer) {
	t.Helper()
	i, out, _ := themedInput(t, th, 40, 5, keys...)
	i.Password = true
	return i, out
}

func TestThemedPasswordMasksWithThemeSymbol(t *testing.T) {
	th := terst.ASCIITheme()
	th.Symbols.Mask = "•"
	i, out := themedPassword(t, th, "abc", "\x1b[D", "\r")
	got, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "abc", got)
	assert.Contains(t, out.String(), "\n\r| •••\x1b[K\x1b[1D")
	// a password retains no answer line
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[1A\r"))
	assert.True(t, !strings.Contains(out.String(), "abc"))
}

func TestThemedPasswordMultiRuneMaskKeepsRuneEditing(t *testing.T) {
	th := terst.ASCIITheme()
	th.Symbols.Mask = "é" // one column, two runes
	// a typed combining sequence is two runes, so two masks; backspace in
	// the middle removes one rune, as without a theme
	i, out := themedPassword(t, th, "aéb", "\x1b[D", "\x7f", "\r")
	got, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "aeb", got)
	assert.Contains(t, out.String(), "| éééé\x1b[K\x1b[1D")
	assert.Contains(t, out.String(), "| ééé\x1b[K\x1b[1D")
}

func TestThemedPasswordMaskClipsToWindow(t *testing.T) {
	th := terst.ASCIITheme()
	th.Inline = true
	th.Symbols.Mask = "#"
	terst.UseTheme(t, th)
	i := newInput(t.Context(), "Label")
	i.Password = true
	i.typed, i.cursor = "secret", 6
	// "? Label " takes 8 of 12 columns, the last one is the caret's: 3 remain
	got, err := renderThemedInput(t, i, 12, 5)
	assert.NoError(t, err)
	assert.Equal(t, "\r? Label ###\x1b[K", got)
}

func TestThemedPasswordRejectionIsSecretIndependent(t *testing.T) {
	leak := WithValidation(func(_ context.Context, s string) error {
		if s == "hunter2" {
			return fmt.Errorf("%q is too weak", s)
		}
		return nil
	})
	var seen []string
	th := terst.ASCIITheme()
	th.Funcs = map[string]any{"spy": func(s string) string { seen = append(seen, s); return s }}
	th.Password.Label = `{{ spy .Label }}`
	th.Password.Feedback = `{{ error (spy .Message) }}`
	for _, tc := range []struct {
		name string
		opts []option.Opt
		want string
	}{
		{"theme slot", nil, "\n\r| Invalid value\x1b[K"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen = nil
			i, out := themedPassword(t, th, "hunter2", "\r", "!", "\r")
			got, err := i.read(t.Context(), append([]option.Opt{leak}, tc.opts...)...)
			assert.NoError(t, err)
			assert.Equal(t, "hunter2!", got)
			assert.Contains(t, out.String(), tc.want)
			assert.True(t, !strings.Contains(out.String(), "hunter2"))
			for _, s := range seen {
				assert.True(t, !strings.Contains(s, "hunter2"))
			}
		})
	}
}

func TestThemedPasswordShowsBuiltInRuleMessages(t *testing.T) {
	i, out := themedPassword(t, terst.ASCIITheme(), "\r", "ab", "\r", "c", "\r")
	got, err := i.read(t.Context(), WithNonEmpty(), WithRegex(`.{3,}`, "at least 3 characters"))
	assert.NoError(t, err)
	assert.Equal(t, "abc", got)
	assert.Contains(t, out.String(), "\n\r| ! a value is required\x1b[K")
	assert.Contains(t, out.String(), "\n\r| ! at least 3 characters\x1b[K")
}

func TestThemedPasswordFailingFeedbackKeepsSecretOut(t *testing.T) {
	th := terst.ASCIITheme()
	th.Funcs = map[string]any{"fails": func(s string) (string, error) { return "", fmt.Errorf("cannot show %q", s) }}
	th.Password.Feedback = `{{ fails .Message }}`
	leak := WithValidation(func(_ context.Context, s string) error { return errors.New(s) })
	i, out := themedPassword(t, th, "hunter2", "\r")
	_, err := i.read(t.Context(), leak)
	assert.Contains(t, err.Error(), "theme.Password.Feedback")
	assert.Contains(t, err.Error(), `cannot show "Invalid value"`)
	assert.True(t, !strings.Contains(err.Error(), "hunter2"))
	assert.True(t, !strings.Contains(out.String(), "hunter2"))
}
