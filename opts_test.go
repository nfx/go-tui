// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

type optsDummy struct {
	config
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
	if d.in != reader {
		t.Fatalf("reader not set")
	}
	if d.out != writer {
		t.Fatalf("writer not set")
	}
}

func TestWithContextAndTimeout(t *testing.T) {
	base := t.Context()
	d := &optsDummy{}

	if err := WithContext(base)(d); err != nil {
		t.Fatalf("WithContext failed: %v", err)
	}
	if d.ctx != base {
		t.Fatalf("context not set")
	}

	if err := WithTimeout(10 * time.Millisecond)(d); err != nil {
		t.Fatalf("WithTimeout failed: %v", err)
	}
	deadline, ok := d.ctx.Deadline()
	if !ok {
		t.Fatalf("expected deadline set on context")
	}
	if deadline.Before(time.Now()) {
		t.Fatalf("deadline should be in the future")
	}
}

func TestOptTRejectsWrongWidget(t *testing.T) {
	err := WithOutput(&bytes.Buffer{})("nope")
	assert.ErrorIs(t, err, ErrWrongWidget)
	assert.Contains(t, err.Error(), "need tui.withIO, got string")
}

func TestWithOptionsSkipsUnsupportedMembers(t *testing.T) {
	writer := &bytes.Buffer{}
	d := &optsDummy{}
	err := WithOptions(WithHide(), WithOutput(writer), WithWorkers(2))(d)
	assert.NoError(t, err)
	assert.Equal(t, io.Writer(writer), d.out)
}

func TestWithOptionsFailsWhenNoMemberApplies(t *testing.T) {
	err := WithOptions(WithHide(), WithWorkers(2))(&optsDummy{})
	assert.ErrorIs(t, err, ErrWrongWidget)
}

func TestWithOptionsEmptyIsNoop(t *testing.T) {
	assert.NoError(t, WithOptions()(&optsDummy{}))
}

func TestWithOptionsPropagatesInvalidValues(t *testing.T) {
	err := WithOptions(WithWorkers(0))(newProgressbar())
	assert.ErrorIs(t, err, ErrInvalidState)
	assert.True(t, !errors.Is(err, ErrWrongWidget))
}

func TestEntryPointsRejectOptionsOfOtherWidgets(t *testing.T) {
	_, err := Input("Name", WithWorkers(2))
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = Dropdown("Pick", []string{"a"}, WithNonEmpty())
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = NewMaxProgressBar("bar", 1, WithHide())
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = NewSpinners(WithPrefixf("group"))
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = newSpinners().Add(t.Context(), WithContext(t.Context()))
	assert.ErrorIs(t, err, ErrWrongWidget)

	_, err = FilePicker("Pick", WithWorkers(2))
	assert.ErrorIs(t, err, ErrWrongWidget)
}

func TestSharedTemplateOptionsConfigureInput(t *testing.T) {
	i := newInput("Name")
	err := opts{
		WithHide(),
		WithLabelTemplate("label"),
		WithAnswerTemplate("answer"),
	}.Apply(i)
	assert.NoError(t, err)
	assert.True(t, i.Hide)
	assert.Equal(t, "label", i.LabelTemplate)
	assert.Equal(t, "answer", i.AnswerTemplate)
}

func TestFilePickerSplitsOptionsBetweenParts(t *testing.T) {
	writer := &bytes.Buffer{}
	f, d, err := newFilePicker(
		WithStartDir(t.TempDir()),
		WithOutput(writer),
		WithOptions(WithShowHidden(), WithHide()),
	)
	assert.NoError(t, err)
	assert.True(t, f.showHidden)
	assert.True(t, d.Hide)
	assert.Equal(t, io.Writer(writer), d.out)
}
