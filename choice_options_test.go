// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
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
	err := WithDefault("alpha")(d)
	assert.NoError(t, err)
	assert.Equal(t, "alpha", d.oneMatch)
}

func TestWithOneMatch_singlePrefixMatchReturnsIndex(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 2, Name: "beta"},
	}
	err := WithDefault("beta")(d)
	assert.NoError(t, err)
	bindDropdown(t, d)
	_, err = d.renderInit(terst.NewTermIO(20, 6))
	var oneMatch oneHatch
	ok := errors.As(err, &oneMatch)
	assert.True(t, ok)
	// a unique prefix skips ranking, so the position is in item order
	assert.Equal(t, 1, int(oneMatch))
	assert.Equal(t, []int{0, 1}, d.relevant)
	assert.Equal(t, "", d.oneMatch)
}

func TestWithOneMatch_noMatchKeepsDropdown(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 2, Name: "beta"},
	}
	err := WithDefault("gamma")(d)
	assert.NoError(t, err)
	assert.NoError(t, d.parseTemplates())
	_, err = d.renderInit(terst.NewTermIO(20, 6))
	assert.NoError(t, err)
	assert.Equal(t, 2, len(d.Items))
	assert.Equal(t, "", d.oneMatch)
}

func TestWithOneMatch_sortsByLevensteinDistance(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "zeta"},
		dropdownHeuristicLabelItem{ID: 2, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 3, Name: "omega"},
	}
	err := WithDefault("alpa")(d)
	assert.NoError(t, err)
	bindDropdown(t, d)
	_, err = d.renderInit(terst.NewTermIO(20, 6))
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 0, 2}, d.relevant)
	assert.Equal(t, []int{1, 0, 2}, d.displayed)
	assert.Equal(t, "", d.oneMatch)
}

func TestWithOneMatch_multipleMatchesKeepDropdown(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "same"},
		dropdownHeuristicLabelItem{ID: 2, Name: "same"},
	}
	err := WithDefault("same")(d)
	assert.NoError(t, err)
	assert.NoError(t, d.parseTemplates())
	_, err = d.renderInit(terst.NewTermIO(20, 6))
	assert.NoError(t, err)
	assert.Equal(t, 2, len(d.Items))
	assert.Equal(t, "", d.oneMatch)
}

func TestDropdownRemovedColorNamesNameTheOption(t *testing.T) {
	_, err := Dropdown(t.Context(), "x", []string{"a"}, WithTemplate(`red .`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `WithTemplate content:1: function "red" not defined`)
}
