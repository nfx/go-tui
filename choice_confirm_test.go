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
	"text/template"

	"github.com/nfx/go-tui/internal/assert"
)

func TestConfirmfRunsWithFormattedAction(t *testing.T) {
	orig := confirmRunner
	defer func() { confirmRunner = orig }()
	confirmRunner = func(ctx context.Context, action string, opts ...opt) bool {
		assert.Equal(t, "Proceed with task?", action)
		return true
	}

	assert.True(t, Confirmf(t.Context(), "Proceed with %s?", "task"))
}

func TestDefaultConfirmRunnerReturnsTrue(t *testing.T) {
	in := bytes.NewBuffer([]byte{keyEnter})
	out := &bytes.Buffer{}
	opt := opT(func(d *dropdown) error {
		d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return &termIO{
				in:      in,
				out:     out,
				Width:   20,
				Height:  6,
				Restore: func() error { return nil },
			}, nil
		}
		return nil
	})
	assert.True(t, defaultConfirmRunner(t.Context(), "Proceed?", WithInput(in), WithOutput(out), opt))
}

func TestDefaultConfirmRunnerReturnsFalseOnError(t *testing.T) {
	opt := opT(func(d *dropdown) error {
		d.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) {
			return nil, io.EOF
		}

		return nil
	})
	assert.True(t, !defaultConfirmRunner(t.Context(), "Proceed?", opt))
}

func TestConfirmDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault(true)))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault(false)))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("Yes")))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("y")))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault("no")))
	assert.True(t, !Confirm(ctx, "Go?", io))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault(42)))
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	assert.True(t, !Confirm(cctx, "Go?", io, WithDefault(true)))

	// the error behind the false is visible one level down
	_, err := Dropdown(ctx, "Go?", []string{"Yes", "No"}, io)
	assert.ErrorIs(t, err, ErrNoTTY)
}

func TestThemedConfirmGuidedFrameAndYes(t *testing.T) {
	o, out, restored := themedDropdownOpt(t, asciiTheme(false), 40, "\r")
	assert.True(t, Confirm(t.Context(), "Sure?", o))
	assert.True(t, *restored)
	assert.Equal(t, ""+
		"\r? Sure?\n\r| > Yes\n\r|   No\n\r| ^v move · type filter\n\r"+
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+
		"+ Sure? · Yes\n", out.String())
}

func TestThemedConfirmNoIsNormalAnswer(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 40, "\x1b[B", "\r")
	assert.True(t, !Confirm(t.Context(), "Sure?", o))
	// No is retained with the completion mark, not as an error
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[3A\r+ Sure? · No\n"))
	assert.True(t, !strings.Contains(out.String(), "!"))

	o, out, _ = themedDropdownOpt(t, asciiTheme(true), 40, "n", "\r")
	assert.True(t, !Confirm(t.Context(), "Sure?", o))
	assert.True(t, strings.HasPrefix(out.String(), "\r? Sure? > Yes\n\r          No\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Sure? · No\n"))
}

func TestThemedConfirmfFormatsLabel(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 40, "\r")
	prev := confirmRunner
	t.Cleanup(func() { confirmRunner = prev })
	confirmRunner = func(ctx context.Context, action string, _ ...opt) bool {
		return defaultConfirmRunner(ctx, action, o)
	}
	assert.True(t, Confirmf(t.Context(), "Deploy %s?", "api"))
	assert.True(t, strings.HasPrefix(out.String(), "\r? Deploy api?\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Deploy api? · Yes\n"))
}

func TestThemedConfirmSlotPrecedence(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Prompt.Answer = `{{ .Label }} {{ .Value }}/{{ .Count }}`
	th.Templates.Choice.Inactive = `-{{ .Value }}`
	th.Templates.Dropdown.Active = `dropdown {{ .Value }}`
	th.Templates.Dropdown.Label = `dropdown {{ .Label }}`
	th.Templates.Confirm.Label = `{{ .Label }} [y/n]`
	th.Templates.Confirm.Hint = Hidden
	o, out, _ := themedDropdownOpt(t, th, 40, "\x1b[B", "\r")
	assert.True(t, !Confirm(t.Context(), "Sure?", o))
	// shared and Confirm edits apply, Dropdown edits stay with the dropdown
	assert.True(t, strings.HasPrefix(out.String(), "\rSure? [y/n]\n\r| > Yes\n\r|   -No\n\r"+
		"\x1b[3A"))
	assert.True(t, strings.HasSuffix(out.String(), "Sure? No/1\n"))
}

func TestThemedConfirmHiddenAnswer(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Confirm.Answer = Hidden
	o, out, _ := themedDropdownOpt(t, th, 40, "\r")
	assert.True(t, Confirm(t.Context(), "Sure?", o))
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[3A\r"))
}

func TestThemedConfirmDefaultsWithoutTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	// without a terminal only the projection runs, never a presentation slot
	th.Templates.Prompt.Label = `{{ fails .Label }}`
	th.Templates.Confirm.Active = `{{ fails .Value }}`
	th.Templates.Confirm.Inactive = `{{ fails .Value }}`
	th.Templates.Confirm.Answer = `{{ fails .Value }}`
	useTheme(t, th)
	io := quiet(t)
	ctx := t.Context()
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault(true)))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault(false)))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("Yes")))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("y")))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault("no")))
	assert.True(t, !Confirm(ctx, "Go?", io))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault(42)))

	// a Confirm projection is what hints match; an exact boolean still wins
	th.Templates.Confirm.Value = `{{ if eq . "Yes" }}sure{{ else }}never{{ end }}`
	useTheme(t, th)
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("s")))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault("never")))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault(true)))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault("y")))
}

func TestThemedConfirmHelperErrorIsFalseAndRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Templates.Confirm.Inactive = `{{ fails .Value }}`
	o, out, restored := themedDropdownOpt(t, th, 40, "\r")
	assert.True(t, !Confirm(t.Context(), "Sure?", o))
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Sure?"))

	// the same theme leaves a dropdown alone
	o, _, _ = themedDropdownOpt(t, th, 40, "\r")
	_, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
}

func TestThemedConfirmRolesMarkYesAndNoAsCompleted(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	th := asciiTheme(false)
	th.Light = true
	for _, keys := range [][]string{{"\r"}, {"\x1b[B", "\r"}} {
		o, out, _ := themedDropdownOpt(t, th, 40, keys...)
		Confirm(t.Context(), "Sure?", o)
		roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), out, nil).roles
		assert.True(t, len(roles.success) > 0 && len(roles.failure) > 0)
		answer := out.String()[strings.LastIndex(out.String(), "\x1b[3A\r")+len("\x1b[3A\r"):]
		assert.True(t, strings.HasPrefix(answer, string(roles.success)+"+"))
		assert.True(t, !strings.Contains(answer, string(roles.failure)))
	}
}
