// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

const (
	keyUp   = "\x1b[A"
	keyDown = "\x1b[B"
	keyLeft = "\x1b[D"
)

type port uint16

func TestNumberConvertBoundaries(t *testing.T) {
	check := func(t *testing.T, got, want any, err error) {
		t.Helper()
		assert.NoError(t, err)
		assert.Equal(t, want, got)
	}
	i8, err := newNumber[int8](t.Context(), "").convert("-128")
	check(t, i8, int8(-128), err)
	_, err = newNumber[int8](t.Context(), "").convert("128")
	assert.Error(t, err)
	u16, err := newNumber[port](t.Context(), "").convert("+65535")
	check(t, u16, port(65535), err)
	_, err = newNumber[port](t.Context(), "").convert("65536")
	assert.Error(t, err)
	u64, err := newNumber[uint64](t.Context(), "").convert("18446744073709551615")
	check(t, u64, uint64(math.MaxUint64), err)
	_, err = newNumber[uint](t.Context(), "").convert("-0")
	assert.Error(t, err)
	i64, err := newNumber[int64](t.Context(), "").convert("9223372036854775807")
	check(t, i64, int64(math.MaxInt64), err)
	f32, err := newNumber[float32](t.Context(), "").convert("0.1")
	check(t, f32, float32(0.1), err)
	_, err = newNumber[float64](t.Context(), "").convert("5e-324")
	assert.Error(t, err) // exponents are outside of the grammar
	f64, err := newNumber[float64](t.Context(), "").convert("." + strings.Repeat("0", 323) + "5")
	check(t, f64, math.SmallestNonzeroFloat64, err)
	_, err = newNumber[float64](t.Context(), "").convert("." + strings.Repeat("0", 330) + "1")
	assert.Error(t, err) // underflow to zero
	f64, err = newNumber[float64](t.Context(), "").convert("0.000")
	check(t, f64, 0.0, err)
	_, err = newNumber[float32](t.Context(), "").convert("340282350000000000000000000000000000001")
	assert.NoError(t, err)
	_, err = newNumber[float32](t.Context(), "").convert("1" + strings.Repeat("0", 39))
	assert.Error(t, err)
	_, err = newNumber[float64](t.Context(), "").convert("1" + strings.Repeat("0", 400))
	assert.Error(t, err)
	for _, bad := range []string{"", "-", ".", "1..2", "1_0", "NaN", "Inf", "0x10", "١", "1 ", "+-1", "1.5."} {
		_, err = newNumber[float64](t.Context(), "").convert(bad)
		assert.Error(t, err)
	}
	for _, ok := range []string{"1.", ".5", "+1.25", "-.5", "007"} {
		_, err = newNumber[float64](t.Context(), "").convert(ok)
		assert.NoError(t, err)
	}
	_, err = newNumber[int](t.Context(), "").convert("1.5")
	assert.Error(t, err)
}

func TestNumberNativeWidth(t *testing.T) {
	n := newNumber[int](t.Context(), "")
	assert.Equal(t, strconv.IntSize, n.bits)
	assert.True(t, n.signed)
	u := newNumber[uint](t.Context(), "")
	assert.Equal(t, strconv.IntSize, u.bits)
	assert.True(t, !u.signed)
}

// numberAnswer is what a number prompt returned.
type numberAnswer[T Numeric] struct {
	v   T
	err error
}

// startNumber runs a number prompt on a test terminal, for keys typed while
// it waits.
func startNumber[T Numeric](t *testing.T, opts ...option.Opt) (*terst.Keys, *terst.Output, <-chan numberAnswer[T]) {
	t.Helper()
	keys, out := terst.UseTerminal(t)
	answer := make(chan numberAnswer[T], 1)
	go func() {
		v, err := Number[T](t.Context(), "Count:", opts...)
		answer <- numberAnswer[T]{v, err}
	}()
	return keys, out, answer
}

// runNumber answers a number prompt with keys, then the end of input.
func runNumber[T Numeric](t *testing.T, keys string, opts ...option.Opt) (T, *terst.Output, error) {
	t.Helper()
	in, out, answer := startNumber[T](t, opts...)
	in.Type(keys)
	in.Close()
	got := terst.Recv(t, answer)
	return got.v, out, got.err
}

func TestNumberTypedAndAdjusted(t *testing.T) {
	v, out, err := runNumber[int](t, "41"+keyUp+"\r")
	assert.NoError(t, err)
	assert.Equal(t, 42, v)
	out.AwaitOutput("42")

	u, _, err := runNumber[uint64](t, "18446744073709551614"+keyUp+"\r")
	assert.NoError(t, err)
	assert.Equal(t, uint64(math.MaxUint64), u)

	f, out, err := runNumber[float32](t, "1.25"+keyLeft+keyLeft+keyUp+"\r")
	assert.NoError(t, err)
	assert.Equal(t, float32(1.35), f)
	out.AwaitOutput("1.35")

	p, _, err := runNumber[port](t, keyUp+keyUp+"\r", option.WithDefault("8080"))
	assert.NoError(t, err)
	assert.Equal(t, port(8082), p)

	// caret in the default: 1.00 keeps its spelling when submitted
	f64, out, err := runNumber[float64](t, "\r", option.WithDefault("1.00"))
	assert.NoError(t, err)
	assert.Equal(t, 1.0, f64)
	out.AwaitOutput("1.00")
}

func TestNumberInvalidDraftStaysEditable(t *testing.T) {
	// "x" is rejected on Enter, then removed and replaced
	keys, out, answer := startNumber[int](t)
	keys.Type("x\r")
	out.AwaitOutput("enter a whole number")
	keys.Type("\x7f7\r")
	got := terst.Recv(t, answer)
	assert.NoError(t, got.err)
	assert.Equal(t, 7, got.v)
	out.AwaitVisible("✔ Count: · 7")

	// an empty answer is rejected instead of returning zero
	keys, out, answer = startNumber[int](t)
	keys.Type("\r")
	out.AwaitOutput("a number is required")
	keys.Type("5\r")
	got = terst.Recv(t, answer)
	assert.NoError(t, got.err)
	assert.Equal(t, 5, got.v)

	// an invalid default is an editable draft
	v8, _, err := runNumber[int8](t, "\r\x7f\x7f\x7f9\r", option.WithDefault("300"))
	assert.NoError(t, err)
	assert.Equal(t, int8(9), v8)
}

func TestNumberArrowFeedback(t *testing.T) {
	keys, out, answer := startNumber[uint](t)
	keys.Type(keyDown)
	out.AwaitOutput("value cannot go below zero")
	keys.Type("3\r")
	assert.NoError(t, terst.Recv(t, answer).err)

	keys, out, answer2 := startNumber[int](t)
	keys.Type("1x" + keyUp)
	out.AwaitOutput("fix the number before using the arrows")
	keys.Type("\x7f\r")
	assert.NoError(t, terst.Recv(t, answer2).err)
}

func TestNumberCaretMovementKeepsFeedback(t *testing.T) {
	n := newNumber[uint](t.Context(), "n")
	n.prompt.SetDefault("0")
	n.prompt.reject(n.adjust('↓'))
	assert.Equal(t, errNumberBelowZero.Error(), n.prompt.rejection)
	n.prompt.pressLeft()
	assert.Equal(t, errNumberBelowZero.Error(), n.prompt.rejection)
	assert.NoError(t, n.adjust('↑'))
	assert.Equal(t, "", n.prompt.rejection)
	assert.Equal(t, "1", n.prompt.typed)
}

// foreign is an option for a widget that is not a number.
var foreign = option.OpT(func(*struct{}) error { return nil })

func TestNumberOptions(t *testing.T) {
	v, _, err := runNumber[int](t, "\r", option.WithDefault("5"))
	assert.NoError(t, err)
	assert.Equal(t, 5, v)

	for _, o := range []option.Opt{WithNonEmpty(), option.WithHide(), WithValidation(func(context.Context, string) error { return nil }),
		WithRegex(`[0-9]`, "one digit"), foreign, option.WithOptions(foreign)} {
		_, _, err = runNumber[int](t, "5\r", o)
		assert.ErrorIs(t, err, errs.ErrWrongWidget)
	}
	// a bundle applies the members that Number supports
	v, _, err = runNumber[int](t, "\r", option.WithOptions(option.WithDefault("6"), option.WithHide()))
	assert.NoError(t, err)
	assert.Equal(t, 6, v)
}

func TestNumberErrorsReturnZero(t *testing.T) {
	v, out, err := runNumber[int](t, "12")
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, 0, v)
	out.AwaitVisible()
	assert.NotContains(t, out.String(), "Count: 12")

	v, _, err = runNumber[int](t, "12\x03")
	assert.Error(t, err)
	assert.Equal(t, 0, v)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	keys := terst.UseKeys(t)
	keys.Type("1\r")
	f, err := Number[float64](ctx, "x")
	assert.Error(t, err)
	assert.Equal(t, 0.0, f)
}

func TestNumberSharesReaderWithInput(t *testing.T) {
	keys := terst.UseKeys(t)
	keys.Type("7\rhello\r")
	n, err := Number[int](t.Context(), "n")
	assert.NoError(t, err)
	assert.Equal(t, 7, n)
	s, err := Input(t.Context(), "s")
	assert.NoError(t, err)
	assert.Equal(t, "hello", s)
}

func TestInputIgnoresArrows(t *testing.T) {
	keys, out := terst.UseTerminal(t)
	keys.Type("a" + keyUp + keyDown + "b\r")
	s, err := Input(
		t.Context(),
		"s",
	)
	assert.NoError(t, err)
	assert.Equal(t, "ab", s)
	out.AwaitVisible("✔ s · ab")
	assert.NotContains(t, out.String(), "Error")
	keys.Type("a" + keyUp + "b\r")
	p, err := Password(
		t.Context(),
		"p",
	)
	assert.NoError(t, err)
	assert.Equal(t, "ab", p)
}

type tcpPort int

func TestNumberDefaults(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()

	got, err := Number[int](ctx, "Port", option.WithDefault(8080))
	assert.NoError(t, err)
	assert.Equal(t, 8080, got)

	named, err := Number[tcpPort](ctx, "Port", option.WithDefault(tcpPort(80)))
	assert.NoError(t, err)
	assert.Equal(t, tcpPort(80), named)

	big, err := Number[int64](ctx, "Big", option.WithDefault(int64(9007199254740993)))
	assert.NoError(t, err)
	assert.Equal(t, int64(9007199254740993), big)

	f, err := Number[float64](ctx, "Float", option.WithDefault(1e21))
	assert.NoError(t, err)
	assert.Equal(t, 1e21, f)

	f32, err := Number[float32](ctx, "Float", option.WithDefault(float32(0.1)))
	assert.NoError(t, err)
	assert.Equal(t, float32(0.1), f32)

	text, err := Number[int](ctx, "Port", option.WithDefault("42"))
	assert.NoError(t, err)
	assert.Equal(t, 42, text)

	zero, err := Number[int8](ctx, "Small", option.WithDefault(300))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	assert.Equal(t, int8(0), zero)

	_, err = Number[int](ctx, "Port", option.WithDefault("abc"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	_, err = Number[int](ctx, "Port", option.WithDefault(5), WithAtLeast(10))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	got, err = Number[int](ctx, "Port", option.WithDefault(10), WithAtLeast(10), WithAtMost(10))
	assert.NoError(t, err)
	assert.Equal(t, 10, got)

	_, err = Number[int](ctx, "Port")
	assert.ErrorIs(t, err, errs.ErrNoTTY)

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = Number[int](cctx, "Port", option.WithDefault(1))
	assert.ErrorIs(t, err, context.Canceled)
}

// themedNumber is a Number rendering through th on a scripted terminal of the
// given size. Each key is one read. restored reports a released terminal.
func themedNumber[T Numeric](
	t *testing.T,
	th theme.Theme,
	width, height int,
	keys ...string,
) (n *number[T], out *bytes.Buffer, restored *bool) {
	t.Helper()
	i, out, restored := themedInput(t, th, width, height, keys...)
	n = newNumber[T](t.Context(), "Count")
	n.prompt.In, n.prompt.Out, n.prompt.makeTermIO = i.In, i.Out, i.makeTermIO
	return n, out, restored
}

func TestThemedNumberGuidedFrameHintAndAnswer(t *testing.T) {
	n, out, restored := themedNumber[int](t, terst.ASCIITheme(), 30, 5, "41", keyUp, "\r")
	got, err := n.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.True(t, *restored)
	assert.Equal(t, ""+
		// the hint is a guided row below the value; the caret stays on the value
		"\r? Count\x1b[K\n\r| \x1b[K\n\r| ^v adjust\x1b[K\x1b[1A\x1b[3G"+
		"\x1b[1A\r? Count\x1b[K\n\r| 4\x1b[K\n\r| ^v adjust\x1b[K\x1b[1A\x1b[4G"+
		"\x1b[1A\r? Count\x1b[K\n\r| 41\x1b[K\n\r| ^v adjust\x1b[K\x1b[1A\x1b[5G"+
		"\x1b[1A\r? Count\x1b[K\n\r| 42\x1b[K\n\r| ^v adjust\x1b[K\x1b[1A\x1b[5G"+
		// from the caret row below the frame, then up over all three rows
		"\n\n\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r"+
		"+ Count · 42\n", out.String())
}

func TestThemedNumberInlineHintHasNoGuide(t *testing.T) {
	inline := terst.ASCIITheme()
	inline.Inline = true
	n, out, _ := themedNumber[int](t, inline, 30, 5, "7", "\r")
	_, err := n.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r? Count 7\x1b[K\n\r^v adjust\x1b[K\x1b[1A\x1b[10G")
}

func TestThemedNumberHintBelowRejection(t *testing.T) {
	n, out, _ := themedNumber[int](t, terst.ASCIITheme(), 40, 5, "1", "\r", "\x7f", "5", "\r")
	got, err := n.read(t.Context(), WithAtLeast(2), WithAtMost(10))
	assert.NoError(t, err)
	assert.Equal(t, 5, got)
	// the Number feedback slot sits right below the value, the hint after it
	assert.Contains(t, out.String(), ""+
		"\n\r| 1\x1b[K\n\r| ! value must be between 2 and 10\x1b[K\n\r| ^v adjust\x1b[K\x1b[2A\x1b[4G")
	// editing removes the rejection and erases the stale fourth row
	assert.Contains(t, out.String(), "\n\r| \x1b[K\n\r| ^v adjust\x1b[K\n\r\x1b[K\x1b[2A\x1b[3G")
}

func TestThemedNumberShortTerminalsDropHintFirst(t *testing.T) {
	for _, tc := range []struct {
		name      string
		height    int
		rejection string
		want      string
	}{
		{"hint before label", 2, "", "\r? Count\x1b[K\n\r| 3\x1b[K"},
		{"hint before rejection", 3, "no", "\r? Count\x1b[K\n\r| 3\x1b[K\n\r| ! no\x1b[K\x1b[1A\x1b[4G"},
		{"everything fits", 4, "no", "" +
			"\r? Count\x1b[K\n\r| 3\x1b[K\n\r| ! no\x1b[K\n\r| ^v adjust\x1b[K\x1b[2A\x1b[4G"},
		{"one row keeps the value", 1, "", "\r| 3\x1b[K"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _, _ := themedNumber[int](t, terst.ASCIITheme(), 0, 0)
			n.prompt.typed, n.prompt.cursor, n.prompt.rejection = "3", 1, tc.rejection
			got, err := renderThemedInput(t, n.prompt, 30, tc.height)
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestThemedNumberHintSlots(t *testing.T) {
	// the prompt captures the theme when it is made, after installing th
	frame := func(th theme.Theme, build func() *input) string {
		t.Helper()
		terst.UseTheme(t, th)
		i := build()
		i.typed, i.cursor = "1", 1
		got, err := renderThemedInput(t, i, 30, 5)
		assert.NoError(t, err)
		return got
	}
	number := func() *input { return newNumber[int](t.Context(), "Count").prompt }

	// a shared hint reaches Number, which passes its actual key bindings
	th := terst.ASCIITheme()
	th.Prompt.Hint = `{{ range . }}[{{ .Left }}={{ .Right }}]{{ end }}`
	assert.Contains(t, frame(th, number), "\n\r| [^v=adjust]\x1b[K")
	// Input and Password handle no keys beyond editing, so they show no hint
	assert.Equal(t, "\r? Count\x1b[K\n\r| 1\x1b[K", frame(th, func() *input { return newInput(t.Context(), "Count") }))
	assert.Equal(t, "\r? Count\x1b[K\n\r| *\x1b[K", frame(th, func() *input {
		i := newInput(t.Context(), "Count")
		i.Password = true
		return i
	}))

	// a Number override stays local, and Hidden reserves no row
	th = terst.ASCIITheme()
	th.Number.Hint = `number hint`
	assert.Contains(t, frame(th, number), "\n\r| number hint\x1b[K")
	th.Number.Hint = theme.Hidden
	assert.Equal(t, "\r? Count\x1b[K\n\r| 1\x1b[K", frame(th, number))
	// Number inherits an Input hint
	th = terst.ASCIITheme()
	th.Input.Hint = `input hint`
	assert.Contains(t, frame(th, number), "\n\r| input hint\x1b[K")
}

func TestThemedNumberStepsAndRetainsExactDraft(t *testing.T) {
	// float steps at the caret's decimal place and keeps the fractional digits
	n, out, _ := themedNumber[float32](t, terst.ASCIITheme(), 30, 5, "1.25", keyLeft, keyLeft, keyUp, "\r")
	f, err := n.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, float32(1.35), f)
	assert.True(t, strings.HasSuffix(out.String(), "+ Count · 1.35\n"))

	// the submitted spelling is retained, not the converted value
	d, out, _ := themedNumber[float64](t, terst.ASCIITheme(), 30, 5, "\r")
	v, err := d.read(t.Context(), option.WithDefault("1.00"))
	assert.NoError(t, err)
	assert.Equal(t, 1.0, v)
	assert.True(t, strings.HasSuffix(out.String(), "+ Count · 1.00\n"))

	// bounds stop the arrows through the Number feedback slot
	th := terst.ASCIITheme()
	th.Number.Feedback = `number: {{ .Message }}`
	b, out, _ := themedNumber[int](t, th, 40, 5, keyUp, "\r")
	i, err := b.read(t.Context(), option.WithDefault(10), WithAtMost(10))
	assert.NoError(t, err)
	assert.Equal(t, 10, i)
	assert.Contains(t, out.String(), "\n\r| number: value must be at most 10\x1b[K")

	// an unsigned number cannot step below zero
	u, out, _ := themedNumber[uint](t, terst.ASCIITheme(), 40, 5, keyDown, "3", "\r")
	w, err := u.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, uint(3), w)
	assert.Contains(t, out.String(), "| ! value cannot go below zero\x1b[K")
}

func TestThemedNumberKeepsOptionSet(t *testing.T) {
	for _, o := range []option.Opt{foreign} {
		n, _, restored := themedNumber[int](t, terst.ASCIITheme(), 30, 5, "1", "\r")
		_, err := n.read(t.Context(), o)
		assert.ErrorIs(t, err, errs.ErrWrongWidget)
		assert.True(t, !*restored)
	}
}

func TestThemedNumberHintErrorRestoresTerminal(t *testing.T) {
	th := terst.ASCIITheme()
	th.Funcs = map[string]any{"fails": func() (string, error) { return "", errors.New("boom") }}
	th.Number.Hint = `{{ fails }}`
	n, out, restored := themedNumber[int](t, th, 30, 5, "1", "\r")
	_, err := n.read(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Number.Hint")
	assert.True(t, *restored)
	assert.Equal(t, "", out.String())

	// without a terminal, the default is the answer and no slot runs
	terst.StrictOutput(t)
	n, out, _ = themedNumber[int](t, th, 30, 5)
	n.prompt.Out = out
	n.prompt.makeTermIO = func(io.Reader, io.Writer) (*term.IO, error) {
		t.Fatal("acquired a terminal")
		return nil, errs.ErrNoTTY
	}
	v, err := n.read(t.Context(), option.WithDefault(5))
	assert.NoError(t, err)
	assert.Equal(t, 5, v)
	assert.Equal(t, "", out.String())
}
