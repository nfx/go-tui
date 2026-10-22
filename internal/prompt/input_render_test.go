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

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestRenderNormal(t *testing.T) {
	literalInputs(t)
	i := &input{
		Label:  "Enter text:",
		typed:  "hello",
		cursor: 5,
	}

	var out bytes.Buffer
	io := &term.IO{Out: &out}
	bindInput(t, i, io)

	frame := &bytes.Buffer{}
	err := i.render(io, frame)
	assert.NoError(t, err)

	expected := "\rEnter text: hello\x1b[K"
	assert.Equal(t, expected, out.String())
}

func TestRenderCursorAtStart(t *testing.T) {
	literalInputs(t)
	i := &input{
		Label:  "Input:",
		typed:  "abc",
		cursor: 0,
	}

	var out bytes.Buffer
	io := &term.IO{Out: &out}
	bindInput(t, i, io)

	frame := &bytes.Buffer{}
	err := i.render(io, frame)
	assert.NoError(t, err)

	expected := "\rInput: abc\x1b[K\x1b[3D"
	assert.Equal(t, expected, out.String())
}

func TestRenderWriteError(t *testing.T) {
	literalInputs(t)
	i := &input{
		Label:  "Test:",
		typed:  "test",
		cursor: 0,
	}

	// Mock a failing writer
	failingWriter := &failingBuffer{}
	tio := &term.IO{Out: failingWriter}
	bindInput(t, i, tio)

	frame := &bytes.Buffer{}
	err := i.render(tio, frame)
	assert.Error(t, err)
	assert.ErrorIs(t, err, io.EOF)
}

// Helper struct for testing write errors.
type failingBuffer struct{}

func (f *failingBuffer) Write(p []byte) (n int, err error) {
	return 0, io.EOF
}

func TestInputRendersPastedControlsSafely(t *testing.T) {
	literalInputs(t)
	i := &input{
		Label:  "L",
		typed:  "a\rb\x1b[2J\x7f\u009b",
		cursor: 1,
	}
	var out bytes.Buffer
	tt := &term.IO{Out: &out}
	bindInput(t, i, tt)
	err := i.render(tt, &bytes.Buffer{})
	assert.NoError(t, err)
	assert.Equal(t, "\rL a␍b␛[2J␡�\x1b[K\x1b[8D", out.String())
}

// renderInput renders with termWidth content columns plus one spare, or unknown width when zero.
func renderInput(t *testing.T, i *input, termWidth int) string {
	t.Helper()
	literalInputs(t)
	var out bytes.Buffer
	tt := &term.IO{Out: &out}
	if termWidth > 0 { // zero is an unknown width
		tt.Width = termWidth + 1
	}
	bindInput(t, i, tt)
	err := i.render(tt, &bytes.Buffer{})
	assert.NoError(t, err)
	return out.String()
}

// regression: wide runes must be clipped by terminal columns, not rune count
func TestInputRenderClipsWideRunesByWidth(t *testing.T) {
	// "L " takes two of six columns, leaving four for two wide runes
	got := renderInput(t, &input{Label: "L", typed: "\u754c\u754c\u754c\u754c", cursor: 4}, 6)
	assert.Equal(t, "\rL \u754c\u754c\x1b[K", got)
	got = renderInput(t, &input{Label: "L", typed: "\u754c\u754c\u754c\u754c", cursor: 0}, 6)
	assert.Equal(t, "\rL \u754c\u754c\x1b[K\x1b[4D", got)
	got = renderInput(t, &input{Label: "L", typed: "a\u754c\u754c\u754c", cursor: 2}, 6)
	assert.Equal(t, "\rL \u754c\u754c\x1b[K\x1b[2D", got)
}

func TestInputRenderMovesOverCombiningRunes(t *testing.T) {
	got := renderInput(t, &input{Label: "L", typed: "e\u0301e\u0301x", cursor: 2}, 0)
	assert.Equal(t, "\rL e\u0301e\u0301x\x1b[K\x1b[2D", got)
}

func TestInputRenderClipsWithoutOrphanCombiningRune(t *testing.T) {
	// the window cannot hold the "e" under the combining acute accent
	got := renderInput(t, &input{Label: "L", typed: "ae\u0301bcd", cursor: 6}, 5)
	assert.Equal(t, "\rL bcd\x1b[K", got)
}

// regression: a combining mark at the cursor must not render without its base,
// a cursor inside of a cluster stands on the whole cluster.
func TestInputRenderClipsOrphanCombiningRuneAtCursor(t *testing.T) {
	i := &input{typed: "は\u3099ab", cursor: 1}
	start, end := i.visibleWindow(ansi.Text(i.typed).Clusters(), 2)
	assert.Equal(t, 0, start)
	assert.Equal(t, 1, end)
	got := renderInput(t, &input{Label: "L", typed: "は\u3099ab", cursor: 1}, 4)
	assert.Equal(t, "\rL は\u3099\x1b[K\x1b[2D", got)
}

func TestInputRenderMasksPasswordByRune(t *testing.T) {
	got := renderInput(t, &input{Label: "L", typed: "\u754ce\u0301", cursor: 1, Password: true}, 0)
	assert.Equal(t, "\rL ***\x1b[K\x1b[2D", got)
}

func TestInputRenderLabelFillsRow(t *testing.T) {
	// the label is clipped to keep one value column, which shows the caret's cluster
	got := renderInput(t, &input{Label: "Label", typed: "abc", cursor: 1}, 6)
	assert.Equal(t, "\rLabe…b\x1b[K\x1b[1D", got)
}

func TestInputFeedbackSanitizesMessage(t *testing.T) {
	bad := errors.New("line1\nline2\x1b[31m\rtail")
	_, out, err := runValidated(t, false, "a\r", WithValidation(func(context.Context, string) error { return bad }))
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "line1 line2[31m tail")
	assert.True(t, !strings.Contains(out, "\x1b[31mtail"))
}

func TestInputFeedbackClippedToOneRow(t *testing.T) {
	th := theme.New()
	th.Templates.Input.Feedback = `{{ .Message }}`
	terst.UseTheme(t, th)
	_, out, err := runValidated(t, false, "a\r",
		WithValidation(func(context.Context, string) error { return errors.New(strings.Repeat("é", 60)) }))
	assert.ErrorIs(t, err, io.EOF)
	// 40 columns less the guide and the autowrap column
	assert.Contains(t, out, strings.Repeat("é", 36)+"…")
	assert.True(t, !strings.Contains(out, strings.Repeat("é", 37)))
}

func TestInputFeedbackTemplateIsPlain(t *testing.T) {
	th := theme.New()
	th.Templates.Input.Feedback = `!! {{ .Message }}`
	terst.UseTheme(t, th)
	_, out, err := runValidated(t, false, "a\r",
		WithValidation(func(context.Context, string) error { return errors.New("bad") }))
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "!! bad")
	assert.True(t, !strings.Contains(out, "\x1b[31m"))
}

// themedInput is an input rendering through th on a scripted terminal of the
// given size. Each key is one read. restored reports a released terminal.
func themedInput(
	t *testing.T,
	th theme.Theme,
	width, height int,
	keys ...string,
) (i *input, out *bytes.Buffer, restored *bool) {
	t.Helper()
	terst.UseTheme(t, th)
	i = newInput(t.Context(), "Label")
	var chunks [][]byte
	for _, k := range keys {
		chunks = append(chunks, []byte(k))
	}
	out, restored = &bytes.Buffer{}, new(bool)
	i.In, i.Out = &terst.ChunkReader{Chunks: chunks}, out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
		restore := func() error { *restored = true; return nil }
		return &term.IO{In: in, Out: out, Width: width, Height: height, Restore: restore}, nil
	}
	return i, out, restored
}

func TestThemedInputGuidedFrameAnswerAndClear(t *testing.T) {
	i, out, restored := themedInput(t, terst.ASCIITheme(false), 20, 5, "a", "\r")
	got, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
	assert.True(t, *restored)
	assert.Equal(t, ""+
		// the label has its own row, the caret stays on the guided value row
		"\r? Label\x1b[K\n\r| \x1b[K"+
		// the next frame starts from the label row
		"\x1b[1A\r? Label\x1b[K\n\r| a\x1b[K"+
		// from the caret row below the frame, then up over both rows
		"\n\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r"+
		"+ Label · a\n", out.String())
}

func TestThemedInputInlineKeepsLabelOnValueRow(t *testing.T) {
	i, out, _ := themedInput(t, terst.ASCIITheme(true), 20, 5, "a", "\x1b[D", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, ""+
		"\r? Label \x1b[K"+
		"\r? Label a\x1b[K"+
		"\r? Label a\x1b[K\x1b[1D"+
		"\n\x1b[1A\r\x1b[K"+
		"+ Label · a\n", out.String())
}

func TestThemedInputInlineReservesCaretColumn(t *testing.T) {
	// "? Label " takes 8 of 12 columns, the last one is the caret's: 3 remain
	i, out, _ := themedInput(t, terst.ASCIITheme(true), 12, 5, "abcd", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r? Label bcd\x1b[K\n")
}

func TestThemedInputGuidedCaretAfterMovingLeft(t *testing.T) {
	i, out, _ := themedInput(t, terst.ASCIITheme(false), 20, 5, "ab", "\x1b[D", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\x1b[1A\r? Label\x1b[K\n\r| ab\x1b[K\x1b[1D")
}

func TestThemedInputRejectionRowGrowsAndShrinks(t *testing.T) {
	check := WithValidation(func(_ context.Context, s string) error {
		if len(s) < 2 {
			return errors.New("too short")
		}
		return nil
	})
	i, out, _ := themedInput(t, terst.ASCIITheme(false), 20, 5, "a", "\r", "b", "\r")
	got, err := i.read(t.Context(), check)
	assert.NoError(t, err)
	assert.Equal(t, "ab", got)
	assert.Equal(t, ""+
		"\r? Label\x1b[K\n\r| \x1b[K"+
		"\x1b[1A\r? Label\x1b[K\n\r| a\x1b[K"+
		// the rejection adds a guided row below; the caret returns to column 4
		"\x1b[1A\r? Label\x1b[K\n\r| a\x1b[K\n\r| ! too short\x1b[K\x1b[1A\x1b[4G"+
		// typing clears it, and the stale row is erased
		"\x1b[1A\r? Label\x1b[K\n\r| ab\x1b[K\n\r\x1b[K\x1b[1A\x1b[5G"+
		"\n\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r"+
		"+ Label · ab\n", out.String())
}

func TestThemedInputClearsRejectionRowsOnError(t *testing.T) {
	reject := WithValidation(func(context.Context, string) error { return errors.New("no") })
	i, out, restored := themedInput(t, terst.ASCIITheme(false), 20, 5, "a", "\r")
	_, err := i.read(t.Context(), reject)
	assert.ErrorIs(t, err, io.EOF)
	assert.True(t, *restored)
	// three rows, the caret on the second: down two, then up over all three
	assert.True(t, strings.HasSuffix(out.String(),
		"\x1b[1A\x1b[4G\n\n\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r"))
}

// renderThemedInput binds i to a terminal of the given size and draws one frame.
func renderThemedInput(t *testing.T, i *input, width, height int) (string, error) {
	t.Helper()
	var out bytes.Buffer
	tt := &term.IO{Out: &out, Width: width, Height: height}
	assert.NoError(t, i.bind(t.Context(), tt))
	err := i.render(tt, &bytes.Buffer{})
	return out.String(), err
}

func TestThemedInputRejectionTruncatesAndRefitsOnResize(t *testing.T) {
	i, _, _ := themedInput(t, terst.ASCIITheme(false), 0, 0)
	i.typed, i.cursor, i.rejection = "abc", 3, "much too short"
	got, err := renderThemedInput(t, i, 30, 5)
	assert.NoError(t, err)
	assert.Contains(t, got, "\n\r| ! much too short\x1b[K")
	// the same prompt after a resize: the rejection is clipped to the row
	i.rows, i.caretRow = 0, 0
	var out bytes.Buffer
	assert.NoError(t, i.render(&term.IO{Out: &out, Width: 10, Height: 5}, &bytes.Buffer{}))
	assert.Equal(t, "\r? Label\x1b[K\n\r| abc\x1b[K\n\r| ! much\u2026\x1b[K\x1b[1A\x1b[6G", out.String())
}

func TestThemedInputShortTerminals(t *testing.T) {
	for _, tc := range []struct {
		name      string
		width     int
		height    int
		rejection string
		want      string
		err       error
	}{
		{"label dropped first", 20, 2, "no", "\r| a\x1b[K\n\r| ! no\x1b[K\x1b[1A\x1b[4G", nil},
		{"one row keeps the value", 20, 1, "", "\r| a\x1b[K", nil},
		{"rejection needs a second row", 20, 1, "no", "", errs.ErrNoSpace},
		{"no value column", 3, 5, "", "", errs.ErrNoSpace},
		{"unknown size is unbounded", 0, 0, "", "\r? Label\x1b[K\n\r| a\x1b[K", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, _, _ := themedInput(t, terst.ASCIITheme(false), 0, 0)
			i.typed, i.cursor, i.rejection = "a", 1, tc.rejection
			got, err := renderThemedInput(t, i, tc.width, tc.height)
			if tc.err != nil {
				assert.ErrorIs(t, err, tc.err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestThemedInputShrinkKeepsValueOnScreen(t *testing.T) {
	i, _, _ := themedInput(t, terst.ASCIITheme(false), 0, 0)
	i.typed, i.cursor = "a", 1
	got, err := renderThemedInput(t, i, 20, 2)
	assert.NoError(t, err)
	assert.Equal(t, "\r? Label\x1b[K\n\r| a\x1b[K", got)
	// one row left: the label goes, and no newline scrolls the value away
	var out bytes.Buffer
	assert.NoError(t, i.render(&term.IO{Out: &out, Width: 20, Height: 1}, &bytes.Buffer{}))
	assert.Equal(t, "\x1b[1A\r| a\x1b[K", out.String())
	assert.Equal(t, 1, i.rows)
	assert.Equal(t, 0, i.caretRow)
}

func TestThemedInputWideAndCombiningValue(t *testing.T) {
	i, _, _ := themedInput(t, terst.ASCIITheme(false), 0, 0)
	// "| " and the caret column leave five of eight columns: the third wide
	// rune does not fit, and the caret after the cluster moves over two
	i.typed, i.cursor = "e\u0301\u754c\u754c\u754c", 2
	got, err := renderThemedInput(t, i, 8, 5)
	assert.NoError(t, err)
	assert.Equal(t, "\r? Label\x1b[K\n\r| e\u0301\u754c\u754c\x1b[K\x1b[4D", got)
}

func TestThemedInputFamilySlotsRender(t *testing.T) {
	th := terst.ASCIITheme(false)
	th.Templates.Input.Label = `theme {{ .Label }}`
	th.Templates.Input.Answer = `theme {{ .Value }}`
	th.Templates.Input.Feedback = `theme {{ .Message }}`
	reject := WithValidation(func(_ context.Context, s string) error {
		if s == "a" {
			return errors.New("no")
		}
		return nil
	})

	i, out, _ := themedInput(t, th, 20, 5, "a", "\r", "b", "\r")
	_, err := i.read(t.Context(), reject)
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\rtheme Label\x1b[K\n")
	assert.Contains(t, out.String(), "\n\r| theme no\x1b[K")
	assert.True(t, strings.HasSuffix(out.String(), "theme ab\n"))
}

func TestThemedInputHiddenSlots(t *testing.T) {
	th := terst.ASCIITheme(false)
	th.Templates.Prompt.Label = theme.Hidden
	th.Templates.Prompt.Answer = theme.Hidden
	i, out, _ := themedInput(t, th, 20, 5, "a", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	// no label row and no retained answer; the value row stays
	assert.Equal(t, "\r| \x1b[K\r| a\x1b[K\n\x1b[1A\r\x1b[K", out.String())
}

func TestThemedInputFamilySlots(t *testing.T) {
	th := terst.ASCIITheme(false)
	th.Templates.Input.Label = `input`
	th.Templates.Number.Label = `number`
	terst.UseTheme(t, th)
	label := func(i *input) string {
		t.Helper()
		assert.NoError(t, i.bind(t.Context(), &term.IO{Out: &bytes.Buffer{}}))
		return i.label.String()
	}
	assert.Equal(t, "input", label(newInput(t.Context(), "x")))
	password := newInput(t.Context(), "x")
	password.Password = true
	assert.Equal(t, "input", label(password)) // Password inherits Input
	assert.True(t, password.slots().Answer == nil)
	assert.Equal(t, "number", label(newNumber[int](t.Context(), "x").prompt))
}

func TestThemedInputHelperErrorsRestoreTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := terst.ASCIITheme(false)
	th.Funcs = map[string]any{"fails": func(string) (string, error) { return "", boom }}

	th.Templates.Input.Label = `{{ fails .Label }}`
	i, out, restored := themedInput(t, th, 20, 5, "a", "\r")
	_, err := i.read(t.Context())
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Templates.Input.Label")
	assert.True(t, *restored)
	assert.Equal(t, "", out.String()) // nothing drawn

	th.Templates.Input.Label = ""
	th.Templates.Input.Feedback = `{{ fails .Message }}`
	reject := WithValidation(func(context.Context, string) error { return errors.New("no") })
	i, out, restored = themedInput(t, th, 20, 5, "a", "\r")
	_, err = i.read(t.Context(), reject)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	// the frame of the rejection is not drawn, the previous one is cleared
	assert.True(t, strings.HasSuffix(out.String(), "| a\x1b[K\n\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r"))

	th.Templates.Input.Feedback = ""
	th.Templates.Input.Answer = `{{ fails .Value }}`
	i, _, restored = themedInput(t, th, 20, 5, "a", "\r")
	_, err = i.read(t.Context())
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
}

func TestThemedInputDefaultWithoutTerminal(t *testing.T) {
	terst.StrictOutput(t)
	th := terst.ASCIITheme(false)
	th.Templates.Input.Label = `{{ fails .Label }}`
	th.Funcs = map[string]any{"fails": func(string) (string, error) { return "", errors.New("boom") }}
	terst.UseTheme(t, th)
	i := newInput(t.Context(), "Label")
	var out bytes.Buffer
	i.Out = &out
	i.makeTermIO = func(io.Reader, io.Writer) (*term.IO, error) {
		t.Fatal("acquired a terminal")
		return nil, errs.ErrNoTTY
	}
	got, err := i.read(t.Context(), option.WithDefault("dflt"))
	assert.NoError(t, err)
	assert.Equal(t, "dflt", got)
	assert.Equal(t, "", out.String())
}

func TestThemedInputColorsFollowDestination(t *testing.T) {
	terst.WithEnv(t, "FORCE_COLOR=3")
	i, out, _ := themedInput(t, terst.ASCIITheme(false), 20, 5, "a", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	accent := string(i.look.Roles.Accent)
	assert.True(t, strings.HasPrefix(accent, "\x1b[38;2;"))
	assert.Contains(t, out.String(), "\r"+accent+"?\x1b[39m \x1b[1mLabel\x1b[0m\x1b[K\n")
	// the background is unknown, so the value keeps the default and the guide is faint
	assert.Contains(t, out.String(), "\n\r\x1b[2m|\x1b[22m a\x1b[K")

	terst.WithEnv(t)
	i, out, _ = themedInput(t, terst.ASCIITheme(false), 20, 5, "a", "\r")
	_, err = i.read(t.Context())
	assert.NoError(t, err)
	assert.True(t, !strings.Contains(out.String(), "\x1b[3"))
	assert.True(t, !strings.Contains(out.String(), "\x1b[1m"))
}

func TestThemedInputSymbolsFollowEnvironment(t *testing.T) {
	terst.WithEnv(t, "TERM=linux")
	i, out, _ := themedInput(t, theme.New(), 20, 5, "a", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r? Label\x1b[K\n\r| a\x1b[K")

	terst.WithEnv(t)
	i, out, _ = themedInput(t, theme.New(), 20, 5, "a", "\r")
	_, err = i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r◆ Label\x1b[K\n\r│ a\x1b[K")
}

func TestThemedInputCapturesThemeAtConstruction(t *testing.T) {
	i, out, _ := themedInput(t, terst.ASCIITheme(false), 20, 5, "a", "\r")
	assert.NoError(t, term.Default.InstallTheme(terst.ASCIITheme(true)))
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r? Label\x1b[K\n\r| a\x1b[K")
}
