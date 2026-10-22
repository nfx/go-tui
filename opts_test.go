// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"github.com/nfx/go-tui/internal/option"
	"io"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

type optsDummy struct {
	option.Config
}

func TestWithIOOptions(t *testing.T) {
	reader := &bytes.Buffer{}
	writer := &bytes.Buffer{}
	d := &optsDummy{}

	if err := WithInput(reader)(d); err != nil {
		t.Fatalf("WithInput failed: %v", err)
	}
	if err := WithOutput(writer)(d); err != nil {
		t.Fatalf("WithOutput failed: %v", err)
	}
	if d.In != reader {
		t.Fatalf("reader not set")
	}
	if d.Out != writer {
		t.Fatalf("writer not set")
	}
}

func TestOptTRejectsWrongWidget(t *testing.T) {
	err := WithOutput(&bytes.Buffer{})("nope")
	assert.ErrorIs(t, err, ErrWrongWidget)
	assert.Contains(t, err.Error(), "need option.IO, got string")
}

func TestWithOptionsSkipsUnsupportedMembers(t *testing.T) {
	writer := &bytes.Buffer{}
	d := &optsDummy{}
	err := WithOptions(WithHide(), WithOutput(writer), WithWorkers(2))(d)
	assert.NoError(t, err)
	assert.Equal(t, io.Writer(writer), d.Out)
}

func TestWithOptionsFailsWhenNoMemberApplies(t *testing.T) {
	err := WithOptions(WithHide(), WithWorkers(2))(&optsDummy{})
	assert.ErrorIs(t, err, ErrWrongWidget)
}

func TestWithOptionsEmptyIsNoop(t *testing.T) {
	assert.NoError(t, WithOptions()(&optsDummy{}))
}

func TestWithOptionsPropagatesInvalidValues(t *testing.T) {
	err := WithOptions(WithWorkers(0))(newProgressbar(t.Context()))
	assert.ErrorIs(t, err, ErrInvalidState)
	assert.True(t, !errors.Is(err, ErrWrongWidget))
}

func TestEntryPointsRejectOptionsOfOtherWidgets(t *testing.T) {
	_, err := Input(t.Context(), "Name", WithWorkers(2))
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = Dropdown(t.Context(), "Pick", []string{"a"}, WithNonEmpty())
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = NewMaxProgressBar(t.Context(), "bar", 1, WithHide())
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = NewSpinners(t.Context(), WithPrefixf("group"))
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = newSpinners(t.Context()).Add(t.Context(), WithHide())
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = FilePicker(t.Context(), "Pick", WithWorkers(2))
	assert.ErrorIs(t, err, ErrWrongWidget)
}

func TestFilePickerSplitsOptionsBetweenParts(t *testing.T) {
	writer := &bytes.Buffer{}
	f, d, err := newFilePicker(t.Context(),
		WithStartDir(t.TempDir()),
		WithOutput(writer),
		WithOptions(WithShowHidden(), WithHide()),
	)
	assert.NoError(t, err)
	assert.True(t, f.showHidden)
	assert.True(t, d.Hide)
	assert.Equal(t, io.Writer(writer), d.out)
}
