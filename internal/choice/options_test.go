// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/terst"
)

func TestWithTemplateSetsTemplates(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithTemplate(".Name")(d))
	// a projection, which leaves the presentation to the theme
	assert.Equal(t, `{{ __content .Name }}`, d.mainSource)
	assert.Equal(t, "", d.detailsSource)
}

func TestWithTemplateAddsActiveDetails(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithTemplate(".Name", ".Type", ".Owner")(d))
	assert.Equal(t, `{{ __content (.Type) ", " (.Owner) }}`, d.detailsSource)
}

func TestWithOneMatch_setsValue(t *testing.T) {
	d := newDropdown(t.Context())
	err := option.WithDefault("alpha")(d)
	assert.NoError(t, err)
	assert.Equal(t, "alpha", d.oneMatch)
}

func TestWithOneMatch_singlePrefixMatchReturnsIndex(t *testing.T) {
	terst.UseTerminalInput(t, nil)
	i, err := DropdownIndex(t.Context(), "Pick", []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 2, Name: "beta"},
	}, option.WithDefault("beta"))
	assert.NoError(t, err)
	// a unique prefix answers without a frame, at its position in item order
	assert.Equal(t, 1, i)
}

func TestWithOneMatch_noMatchKeepsDropdown(t *testing.T) {
	keys, out, answer := startPick(t, []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 2, Name: "beta"},
	}, option.WithDefault("gamma"))
	out.AwaitVisible("? Pick", "| > alpha", "|   beta", pickFooter)
	keys.Type(terst.KeyEnter)
	got := terst.Recv(t, answer)
	assert.NoError(t, got.err)
	assert.Equal(t, 0, got.index)
}

func TestWithOneMatch_sortsByLevensteinDistance(t *testing.T) {
	keys, out, answer := startPick(t, []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "zeta"},
		dropdownHeuristicLabelItem{ID: 2, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 3, Name: "omega"},
	}, option.WithDefault("alpa"))
	out.AwaitVisible("? Pick", "| > alpha", "|   zeta", "|   omega", pickFooter)
	keys.Type(terst.KeyEnter)
	got := terst.Recv(t, answer)
	assert.NoError(t, got.err)
	assert.Equal(t, 1, got.index)
}

func TestWithOneMatch_multipleMatchesKeepDropdown(t *testing.T) {
	keys, out, answer := startPick(t, []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "same"},
		dropdownHeuristicLabelItem{ID: 2, Name: "same"},
	}, option.WithDefault("same"))
	out.AwaitVisible("? Pick", "| > same", "|   same", pickFooter)
	keys.Type(terst.KeyEnter)
	got := terst.Recv(t, answer)
	assert.NoError(t, got.err)
	assert.Equal(t, 0, got.index)
}

func TestDropdownRemovedColorNamesNameTheOption(t *testing.T) {
	_, err := Dropdown(t.Context(), "x", []string{"a"}, WithTemplate(`red .`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `WithTemplate content:1: function "red" not defined`)
}
