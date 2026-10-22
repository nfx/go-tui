// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	tui "github.com/nfx/go-tui"
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

	if err := tui.WithInput(reader)(d); err != nil {
		t.Fatalf("WithInput failed: %v", err)
	}
	if err := tui.WithOutput(writer)(d); err != nil {
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
	err := tui.WithOutput(&bytes.Buffer{})("nope")
	assert.ErrorIs(t, err, tui.ErrWrongWidget)
	assert.Contains(t, err.Error(), "need option.IO, got string")
}

func TestWithOptionsSkipsUnsupportedMembers(t *testing.T) {
	writer := &bytes.Buffer{}
	d := &optsDummy{}
	err := tui.WithOptions(tui.WithHide(), tui.WithOutput(writer), tui.WithWorkers(2))(d)
	assert.NoError(t, err)
	assert.Equal(t, io.Writer(writer), d.Out)
}

func TestWithOptionsFailsWhenNoMemberApplies(t *testing.T) {
	err := tui.WithOptions(tui.WithHide(), tui.WithWorkers(2))(&optsDummy{})
	assert.ErrorIs(t, err, tui.ErrWrongWidget)
}

func TestWithOptionsEmptyIsNoop(t *testing.T) {
	assert.NoError(t, tui.WithOptions()(&optsDummy{}))
}

func TestEntryPointsRejectOptionsOfOtherWidgets(t *testing.T) {
	_, err := tui.Input(t.Context(), "Name", tui.WithWorkers(2))
	assert.ErrorIs(t, err, tui.ErrWrongWidget)

	_, err = tui.Dropdown(t.Context(), "Pick", []string{"a"}, tui.WithNonEmpty())
	assert.ErrorIs(t, err, tui.ErrWrongWidget)

	_, err = tui.NewMaxProgressBar(t.Context(), "bar", 1, tui.WithHide())
	assert.ErrorIs(t, err, tui.ErrWrongWidget)

	_, err = tui.NewSpinners(t.Context(), tui.WithPrefixf("group"))
	assert.ErrorIs(t, err, tui.ErrWrongWidget)

	group, err := tui.NewSpinners(t.Context(), tui.WithOutput(io.Discard))
	assert.NoError(t, err)
	defer group.Close()
	_, err = group.Add(t.Context(), tui.WithHide())
	assert.ErrorIs(t, err, tui.ErrWrongWidget)

	_, err = tui.FilePicker(t.Context(), "Pick", tui.WithWorkers(2))
	assert.ErrorIs(t, err, tui.ErrWrongWidget)
}
