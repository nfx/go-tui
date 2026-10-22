// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

// stepNumber steps a draft with "|" marking the caret.
func stepNumber[T Numeric](t *testing.T, draft, arrow string) (string, error) {
	t.Helper()
	n := newNumber[T](t.Context(), "n")
	before, after, _ := strings.Cut(draft, "|")
	n.prompt.SetDefault(before + after)
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
