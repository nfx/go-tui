// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestInputVisibleWindowNoClipping(t *testing.T) {
	i := &input{cursor: 3}
	start, end := i.visibleWindow(text("hello").clusters(), 10)
	assert.Equal(t, 0, start)
	assert.Equal(t, 5, end)
}

func TestInputVisibleWindowClipsCursorAtStart(t *testing.T) {
	i := &input{cursor: 2}
	start, end := i.visibleWindow(text("abcdefghij").clusters(), 5)
	assert.Equal(t, 0, start)
	assert.Equal(t, 5, end)
}

func TestInputVisibleWindowClipsCursorAtEnd(t *testing.T) {
	i := &input{cursor: 9}
	start, end := i.visibleWindow(text("abcdefghij").clusters(), 5)
	assert.Equal(t, 5, start)
	assert.Equal(t, 10, end)
}

func TestInputVisibleWindowClipsCursorInMiddle(t *testing.T) {
	i := &input{cursor: 5}
	start, end := i.visibleWindow(text("abcdefghij").clusters(), 4)
	assert.Equal(t, 3, start)
	assert.Equal(t, 7, end)
}

func TestInputVisibleWindowZeroAvail(t *testing.T) {
	i := &input{cursor: 3}
	start, end := i.visibleWindow(text("hello").clusters(), 0)
	assert.Equal(t, 3, start)
	assert.Equal(t, 3, end)
}
