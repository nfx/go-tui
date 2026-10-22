// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

const (
	keyUp   = "\x1b[A"
	keyDown = "\x1b[B"
	keyLeft = "\x1b[D"
)

type port uint16

// stepNumber steps a draft with "|" marking the caret.
func stepNumber[T numberType](t *testing.T, draft, arrow string) (string, error) {
	t.Helper()
	n := newNumber[T](t.Context(), "n")
	before, after, _ := strings.Cut(draft, "|")
	n.prompt.setDefault(before + after)
	n.prompt.cursor = len(before)
	err := n.adjust(map[string]rune{"up": '↑', "down": '↓'}[arrow])
	return n.prompt.typed[:n.prompt.cursor] + "|" + n.prompt.typed[n.prompt.cursor:], err
}

func TestNumberFloatSteps(t *testing.T) {
	for _, tt := range []struct{ draft, arrow, want string }{
		{"|12.34", "up", "|22.34"},
		{"1|2.34", "up", "1|3.34"},
		{"12|.34", "down", "11|.34"},
		{"12.|34", "up", "12.|44"},
		{"12.3|4", "up", "12.3|5"},
		{"9.99|", "up", "10.00|"},
		{"-0.|05", "up", "0.|05"},
		{"12.|", "up", "12.|1"},
		{"12.|", "down", "11.|9"},
		{"|10.00", "down", "|0.00"},
		{"|0.00", "down", "-|1.00"},
		{"|.5", "up", "1|.5"},
		{"-|5", "up", "-|4"},
		{"|-5", "up", "|-4"},
		{"-|1", "up", "|0"},
		{"|+1.25", "up", "|2.25"},
		{"007|", "up", "8|"},
		{"1.00|", "down", "0.99|"},
		{"12", "up", "13|"},
		{"-0.1|", "up", "0.0|"},
		{"|", "up", "1|"},
		{"|", "down", "-1|"},
	} {
		got, err := stepNumber[float64](t, tt.draft, tt.arrow)
		assert.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
}

func TestNumberIntegerSteps(t *testing.T) {
	for _, tt := range []struct{ draft, arrow, want string }{
		{"12|", "up", "13|"},
		{"1|2", "up", "1|3"},
		{"|12", "down", "|11"},
		{"9|", "up", "10|"},
		{"1|9", "up", "2|0"},
		{"|0", "down", "-|1"},
		{"-|1", "up", "|0"},
		{"+5|", "up", "6|"},
		{"|", "up", "1|"},
	} {
		got, err := stepNumber[int](t, tt.draft, tt.arrow)
		assert.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
}

func TestNumberStepRejections(t *testing.T) {
	_, err := stepNumber[uint](t, "0|", "down")
	assert.ErrorIs(t, err, errNumberBelowZero)
	_, err = stepNumber[uint8](t, "|", "down")
	assert.ErrorIs(t, err, errNumberBelowZero)
	_, err = stepNumber[uint8](t, "255|", "up")
	assert.Error(t, err)
	_, err = stepNumber[int8](t, "-128|", "down")
	assert.Error(t, err)
	// a draft that is not a number or is out of range stays unchanged
	for _, draft := range []string{"-|", ".|", "1..2|", "1e3|", " 1|", "300|"} {
		got, err := stepNumber[int8](t, draft, "up")
		assert.Error(t, err)
		assert.Equal(t, draft, got)
	}
	// 2^53 + 1 cannot be told from 2^53 as float64
	_, err = stepNumber[float64](t, "9007199254740992|", "up")
	assert.ErrorIs(t, err, errNumberTooSmall)
	_, err = stepNumber[float32](t, "16777216|", "up")
	assert.ErrorIs(t, err, errNumberTooSmall)
	got, err := stepNumber[float32](t, "16777216|", "down")
	assert.NoError(t, err)
	assert.Equal(t, "16777215|", got)
	_, err = stepNumber[float32](t, "3.4028235e38|", "up")
	assert.Error(t, err)
}

func TestNumberLargeIntegersStayExact(t *testing.T) {
	got, err := stepNumber[uint64](t, "18446744073709551614|", "up")
	assert.NoError(t, err)
	assert.Equal(t, "18446744073709551615|", got)
	_, err = stepNumber[uint64](t, "18446744073709551615|", "up")
	assert.Error(t, err)
	got, err = stepNumber[int64](t, "9007199254740993|", "up")
	assert.NoError(t, err)
	assert.Equal(t, "9007199254740994|", got)
	got, err = stepNumber[int64](t, "-9223372036854775807|", "down")
	assert.NoError(t, err)
	assert.Equal(t, "-9223372036854775808|", got)
	_, err = stepNumber[int64](t, "-9223372036854775808|", "down")
	assert.Error(t, err)
}

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

func runNumber[T numberType](t *testing.T, keys string, option ...opt) (T, string, error) {
	t.Helper()
	var out bytes.Buffer
	v, err := Number[T](t.Context(), "Count:", append(option,
		WithInput(strings.NewReader(keys)), WithOutput(&out))...)
	return v, out.String(), err
}

func TestNumberTypedAndAdjusted(t *testing.T) {
	v, out, err := runNumber[int](t, "41"+keyUp+"\r")
	assert.NoError(t, err)
	assert.Equal(t, 42, v)
	assert.Contains(t, out, "42")

	u, _, err := runNumber[uint64](t, "18446744073709551614"+keyUp+"\r")
	assert.NoError(t, err)
	assert.Equal(t, uint64(math.MaxUint64), u)

	f, out, err := runNumber[float32](t, "1.25"+keyLeft+keyLeft+keyUp+"\r")
	assert.NoError(t, err)
	assert.Equal(t, float32(1.35), f)
	assert.Contains(t, out, "1.35")

	p, _, err := runNumber[port](t, keyUp+keyUp+"\r", WithDefault("8080"))
	assert.NoError(t, err)
	assert.Equal(t, port(8082), p)

	// caret in the default: 1.00 keeps its spelling when submitted
	f64, out, err := runNumber[float64](t, "\r", WithDefault("1.00"))
	assert.NoError(t, err)
	assert.Equal(t, 1.0, f64)
	assert.Contains(t, out, "1.00")
}

func TestNumberInvalidDraftStaysEditable(t *testing.T) {
	// "x" is rejected on Enter, then removed and replaced
	v, out, err := runNumber[int](t, "x\r\x7f7\r")
	assert.NoError(t, err)
	assert.Equal(t, 7, v)
	assert.Contains(t, out, "enter a whole number")
	assert.NotContains(t, out, "Count: x")

	// an empty answer is rejected instead of returning zero
	v, out, err = runNumber[int](t, "\r5\r")
	assert.NoError(t, err)
	assert.Equal(t, 5, v)
	assert.Contains(t, out, "a number is required")

	// an invalid default is an editable draft
	v8, _, err := runNumber[int8](t, "\r\x7f\x7f\x7f9\r", WithDefault("300"))
	assert.NoError(t, err)
	assert.Equal(t, int8(9), v8)
}

func TestNumberArrowFeedback(t *testing.T) {
	_, out, err := runNumber[uint](t, keyDown+"3\r")
	assert.NoError(t, err)
	assert.Contains(t, out, "value cannot go below zero")

	_, out, err = runNumber[int](t, "1x"+keyUp+"\x7f\r")
	assert.NoError(t, err)
	assert.Contains(t, out, "fix the number before using the arrows")
}

func TestNumberCaretMovementKeepsFeedback(t *testing.T) {
	n := newNumber[uint](t.Context(), "n")
	n.prompt.setDefault("0")
	n.prompt.reject(n.adjust('↓'))
	assert.Equal(t, errNumberBelowZero.Error(), n.prompt.rejection)
	n.prompt.pressLeft()
	assert.Equal(t, errNumberBelowZero.Error(), n.prompt.rejection)
	assert.NoError(t, n.adjust('↑'))
	assert.Equal(t, "", n.prompt.rejection)
	assert.Equal(t, "1", n.prompt.typed)
}

func TestNumberOptions(t *testing.T) {
	v, _, err := runNumber[int](t, "\r", WithDefault("5"))
	assert.NoError(t, err)
	assert.Equal(t, 5, v)

	for _, o := range []opt{WithNonEmpty(), WithHide(), WithValidation(func(context.Context, string) error { return nil }),
		WithRegex(`[0-9]`, "one digit"), WithOneReturn(), WithOptions(WithTemplate(".Name"))} {
		_, _, err = runNumber[int](t, "5\r", o)
		assert.ErrorIs(t, err, ErrWrongWidget)
	}
	// a bundle applies the members that Number supports
	v, _, err = runNumber[int](t, "\r", WithOptions(WithDefault("6"), WithHide()))
	assert.NoError(t, err)
	assert.Equal(t, 6, v)
}

func TestNumberBounds(t *testing.T) {
	v, out, err := runNumber[int](t, "1\r\x7f11\r\x7f\x7f5\r", WithAtLeast(2), WithAtMost(10))
	assert.NoError(t, err)
	assert.Equal(t, 5, v)
	assert.Contains(t, out, "value must be between 2 and 10")

	_, out, err = runNumber[int](t, "1\r", WithAtLeast(2))
	assert.Error(t, err)
	assert.Contains(t, out, "value must be at least 2")
	_, out, err = runNumber[int](t, "9\r", WithAtMost(2))
	assert.Error(t, err)
	assert.Contains(t, out, "value must be at most 2")

	// bounds are exact and may have another type than the answer
	u, _, err := runNumber[uint64](
		t,
		"18446744073709551615\r",
		WithAtLeast(int8(-5)),
		WithAtMost(uint64(math.MaxUint64)),
	)
	assert.NoError(t, err)
	assert.Equal(t, uint64(math.MaxUint64), u)
	f, _, err := runNumber[float32](t, "0.1\r", WithAtMost(0.1))
	assert.NoError(t, err)
	assert.Equal(t, float32(0.1), f)

	// a range that leaves no value of T is rejected up front
	_, _, err = runNumber[int8](t, "\r", WithAtLeast(1000))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, _, err = runNumber[uint](t, "\r", WithAtMost(-1))
	assert.ErrorIs(t, err, ErrInvalidOption)
}

func TestNumberBoundsLimitSteps(t *testing.T) {
	v, _, err := runNumber[int](t, "9"+keyUp+keyUp+"\r", WithAtMost(10))
	assert.NoError(t, err)
	assert.Equal(t, 10, v)
	v, _, err = runNumber[int](t, keyDown+keyDown+"\r", WithDefault("1"), WithAtLeast(0))
	assert.NoError(t, err)
	assert.Equal(t, 0, v)
	// outside the range, stepping toward it works
	v, _, err = runNumber[int](t, keyUp+keyUp+"\r", WithAtLeast(2))
	assert.NoError(t, err)
	assert.Equal(t, 2, v)
}

func TestNumberInvalidBounds(t *testing.T) {
	_, _, err := runNumber[int](t, "1\r", WithAtLeast(5), WithAtMost(1))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, _, err = runNumber[int](t, "1\r", WithAtLeast(math.NaN()))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, _, err = runNumber[float32](t, "1\r", WithAtMost(math.MaxFloat64))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = Input(t.Context(), "x", WithAtLeast(1), WithInput(strings.NewReader("")), WithOutput(io.Discard))
	assert.ErrorIs(t, err, ErrWrongWidget)
}

func TestNumberErrorsReturnZero(t *testing.T) {
	v, out, err := runNumber[int](t, "12")
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, 0, v)
	assert.NotContains(t, out, "Count: 12")

	v, _, err = runNumber[int](t, "12\x03")
	assert.Error(t, err)
	assert.Equal(t, 0, v)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f, err := Number[float64](ctx, "x", WithInput(strings.NewReader("1\r")), WithOutput(io.Discard))
	assert.Error(t, err)
	assert.Equal(t, 0.0, f)
}

func TestNumberSharesReaderWithInput(t *testing.T) {
	in := strings.NewReader("7\rhello\r")
	var out bytes.Buffer
	n, err := Number[int](t.Context(), "n", WithInput(in), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, 7, n)
	s, err := Input(t.Context(), "s", WithInput(in), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "hello", s)
}

func TestInputIgnoresArrows(t *testing.T) {
	var out bytes.Buffer
	s, err := Input(t.Context(), "s", WithInput(strings.NewReader("a"+keyUp+keyDown+"b\r")), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "ab", s)
	assert.NotContains(t, out.String(), "Error")
	p, err := Password(t.Context(), "p", WithInput(strings.NewReader("a"+keyUp+"b\r")), WithOutput(io.Discard))
	assert.NoError(t, err)
	assert.Equal(t, "ab", p)
}

type tcpPort int

func TestNumberDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()

	got, err := Number[int](ctx, "Port", io, WithDefault(8080))
	assert.NoError(t, err)
	assert.Equal(t, 8080, got)

	named, err := Number[tcpPort](ctx, "Port", io, WithDefault(tcpPort(80)))
	assert.NoError(t, err)
	assert.Equal(t, tcpPort(80), named)

	big, err := Number[int64](ctx, "Big", io, WithDefault(int64(9007199254740993)))
	assert.NoError(t, err)
	assert.Equal(t, int64(9007199254740993), big)

	f, err := Number[float64](ctx, "Float", io, WithDefault(1e21))
	assert.NoError(t, err)
	assert.Equal(t, 1e21, f)

	f32, err := Number[float32](ctx, "Float", io, WithDefault(float32(0.1)))
	assert.NoError(t, err)
	assert.Equal(t, float32(0.1), f32)

	text, err := Number[int](ctx, "Port", io, WithDefault("42"))
	assert.NoError(t, err)
	assert.Equal(t, 42, text)

	zero, err := Number[int8](ctx, "Small", io, WithDefault(300))
	assert.ErrorIs(t, err, ErrInvalidOption)
	assert.Equal(t, int8(0), zero)

	_, err = Number[int](ctx, "Port", io, WithDefault("abc"))
	assert.ErrorIs(t, err, ErrInvalidOption)

	_, err = Number[int](ctx, "Port", io, WithDefault(5), WithAtLeast(10))
	assert.ErrorIs(t, err, ErrInvalidOption)
	got, err = Number[int](ctx, "Port", io, WithDefault(10), WithAtLeast(10), WithAtMost(10))
	assert.NoError(t, err)
	assert.Equal(t, 10, got)

	_, err = Number[int](ctx, "Port", io)
	assert.ErrorIs(t, err, ErrNoTTY)

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = Number[int](cctx, "Port", io, WithDefault(1))
	assert.ErrorIs(t, err, context.Canceled)
}

// themedNumber is a Number rendering through th on a scripted terminal of the
// given size. Each key is one read. restored reports a released terminal.
func themedNumber[T numberType](
	t *testing.T,
	th Theme,
	width, height int,
	keys ...string,
) (n *number[T], out *bytes.Buffer, restored *bool) {
	t.Helper()
	i, out, restored := themedInput(t, th, width, height, keys...)
	n = newNumber[T](t.Context(), "Count")
	n.prompt.in, n.prompt.out, n.prompt.makeTermIO = i.in, i.out, i.makeTermIO
	return n, out, restored
}

func TestThemedNumberGuidedFrameHintAndAnswer(t *testing.T) {
	n, out, restored := themedNumber[int](t, asciiTheme(false), 30, 5, "41", keyUp, "\r")
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
	n, out, _ := themedNumber[int](t, asciiTheme(true), 30, 5, "7", "\r")
	_, err := n.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r? Count 7\x1b[K\n\r^v adjust\x1b[K\x1b[1A\x1b[10G")
}

func TestThemedNumberHintBelowRejection(t *testing.T) {
	n, out, _ := themedNumber[int](t, asciiTheme(false), 40, 5, "1", "\r", "\x7f", "5", "\r")
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
			n, _, _ := themedNumber[int](t, asciiTheme(false), 0, 0)
			n.prompt.typed, n.prompt.cursor, n.prompt.rejection = "3", 1, tc.rejection
			got, err := renderThemedInput(t, n.prompt, 30, tc.height)
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestThemedNumberHintSlots(t *testing.T) {
	// the prompt captures the theme when it is made, after installing th
	frame := func(th Theme, build func() *input) string {
		t.Helper()
		useTheme(t, th)
		i := build()
		i.typed, i.cursor = "1", 1
		got, err := renderThemedInput(t, i, 30, 5)
		assert.NoError(t, err)
		return got
	}
	number := func() *input { return newNumber[int](t.Context(), "Count").prompt }

	// a shared hint reaches Number, which passes its actual key bindings
	th := asciiTheme(false)
	th.Templates.Prompt.Hint = `{{ range . }}[{{ .Left }}={{ .Right }}]{{ end }}`
	assert.Contains(t, frame(th, number), "\n\r| [^v=adjust]\x1b[K")
	// Input and Password handle no keys beyond editing, so they show no hint
	assert.Equal(t, "\r? Count\x1b[K\n\r| 1\x1b[K", frame(th, func() *input { return newInput(t.Context(), "Count") }))
	assert.Equal(t, "\r? Count\x1b[K\n\r| *\x1b[K", frame(th, func() *input {
		i := newInput(t.Context(), "Count")
		i.Password = true
		return i
	}))

	// a Number override stays local, and Hidden reserves no row
	th = asciiTheme(false)
	th.Templates.Number.Hint = `number hint`
	assert.Contains(t, frame(th, number), "\n\r| number hint\x1b[K")
	th.Templates.Number.Hint = Hidden
	assert.Equal(t, "\r? Count\x1b[K\n\r| 1\x1b[K", frame(th, number))
	// Number inherits an Input hint
	th = asciiTheme(false)
	th.Templates.Input.Hint = `input hint`
	assert.Contains(t, frame(th, number), "\n\r| input hint\x1b[K")
}

func TestThemedNumberStepsAndRetainsExactDraft(t *testing.T) {
	// float steps at the caret's decimal place and keeps the fractional digits
	n, out, _ := themedNumber[float32](t, asciiTheme(false), 30, 5, "1.25", keyLeft, keyLeft, keyUp, "\r")
	f, err := n.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, float32(1.35), f)
	assert.True(t, strings.HasSuffix(out.String(), "+ Count · 1.35\n"))

	// the submitted spelling is retained, not the converted value
	d, out, _ := themedNumber[float64](t, asciiTheme(false), 30, 5, "\r")
	v, err := d.read(t.Context(), WithDefault("1.00"))
	assert.NoError(t, err)
	assert.Equal(t, 1.0, v)
	assert.True(t, strings.HasSuffix(out.String(), "+ Count · 1.00\n"))

	// bounds stop the arrows through the Number feedback slot
	th := asciiTheme(false)
	th.Templates.Number.Feedback = `number: {{ .Message }}`
	b, out, _ := themedNumber[int](t, th, 40, 5, keyUp, "\r")
	i, err := b.read(t.Context(), WithDefault(10), WithAtMost(10))
	assert.NoError(t, err)
	assert.Equal(t, 10, i)
	assert.Contains(t, out.String(), "\n\r| number: value must be at most 10\x1b[K")

	// an unsigned number cannot step below zero
	u, out, _ := themedNumber[uint](t, asciiTheme(false), 40, 5, keyDown, "3", "\r")
	w, err := u.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, uint(3), w)
	assert.Contains(t, out.String(), "| ! value cannot go below zero\x1b[K")
}

func TestThemedNumberKeepsOptionSet(t *testing.T) {
	for _, o := range []opt{WithTemplate("x")} {
		n, _, restored := themedNumber[int](t, asciiTheme(false), 30, 5, "1", "\r")
		_, err := n.read(t.Context(), o)
		assert.ErrorIs(t, err, ErrWrongWidget)
		assert.True(t, !*restored)
	}
}

func TestThemedNumberHintErrorRestoresTerminal(t *testing.T) {
	th := asciiTheme(false)
	th.Funcs = map[string]any{"fails": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Number.Hint = `{{ fails }}`
	n, out, restored := themedNumber[int](t, th, 30, 5, "1", "\r")
	_, err := n.read(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Number.Hint")
	assert.True(t, *restored)
	assert.Equal(t, "", out.String())

	// without a terminal, the default is the answer and no slot runs
	strictOutput(t)
	n, out, _ = themedNumber[int](t, th, 30, 5)
	n.prompt.out = out
	n.prompt.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) {
		t.Fatal("acquired a terminal")
		return nil, ErrNoTTY
	}
	v, err := n.read(t.Context(), WithDefault(5))
	assert.NoError(t, err)
	assert.Equal(t, 5, v)
	assert.Equal(t, "", out.String())
}
