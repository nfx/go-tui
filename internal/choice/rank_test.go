// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestDropdownLevenstein(t *testing.T) {
	d := newDropdown(t.Context())
	assert.Equal(t, 3, d.levenstein([]rune("kitten"), []rune("sitting")))
	assert.Equal(t, 0, d.levenstein([]rune("alpha"), []rune("alpha")))
	assert.Equal(t, 1, d.levenstein([]rune("alpa"), []rune("alpha")))
}

func TestDropdownLevensteinCountsRunes(t *testing.T) {
	d := newDropdown(t.Context())
	assert.Equal(t, 1, d.levenstein([]rune("é"), []rune("a")))
	assert.Equal(t, 1, d.levenstein([]rune("é"), []rune("è")))
	assert.Equal(t, 3, d.levenstein([]rune("sitting"), []rune("kitten")))
	assert.Equal(t, 3, d.levenstein([]rune(""), []rune("abc")))
	assert.Equal(t, 0, d.levenstein([]rune(""), []rune("")))
}

func TestDropdownLevensteinBoundedWork(t *testing.T) {
	d := newDropdown(t.Context())
	long := strings.Repeat("a", 100_000)
	assert.Equal(t, 0, d.levenstein([]rune(long), []rune(long)))
	assert.Equal(t, maxRankRunes, d.levenstein([]rune(long), []rune(strings.Repeat("b", 100_000))))
}
