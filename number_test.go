// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
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
		WithRegex(`[0-9]`, "one digit"), WithOneReturn(), WithOptions(WithFeedbackTemplate("{{.Message}}"))} {
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
