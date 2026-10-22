// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"io"
	"math"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

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
