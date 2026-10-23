// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/tcommon"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestOptTRejectsWrongWidget(t *testing.T) {
	err := tui.WithDefault("x")("nope")
	assert.ErrorIs(t, err, tui.ErrWrongWidget)
	assert.Contains(t, err.Error(), "need option.Default, got string")
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

	tcommon.UseFrames(t)
	group, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	defer group.Close()
	_, err = group.Add(t.Context(), tui.WithHide())
	assert.ErrorIs(t, err, tui.ErrWrongWidget)

	_, err = tui.FilePicker(t.Context(), "Pick", tui.WithWorkers(2))
	assert.ErrorIs(t, err, tui.ErrWrongWidget)
}
