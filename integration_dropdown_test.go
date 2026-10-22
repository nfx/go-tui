// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui_test

import (
	"testing"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/assert"
)

func TestDropdownDefaultSelectsClosestMatch(t *testing.T) {
	term := newASCIITerminal(t)
	res := prompt(func() (string, error) {
		return tui.Dropdown(t.Context(), "Pick", []string{"apple pie", "banana", "apple"},
			tui.WithDefault("apple"))
	})
	// two items share the prefix, so the closest one is ranked first and selected
	term.waitRows(t, "? Pick", "| > apple", "|   apple pie", "|   banana", "| ^v move...")
	term.press(t, keyEnter)
	assert.Equal(t, "apple", answer(t, res))
	term.waitRows(t, "+ Pick · apple")
}

func TestDropdownLazyDefaultSelectsBestRankedArrival(t *testing.T) {
	term := newASCIITerminal(t)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", feed(items), tui.WithDefault("cat"))
	})
	read := term.blockedRead(t)
	items <- "xxxx"
	term.waitRows(t, "? Pick", "| > xxxx", "| ^v move...")
	items <- "yyyy"
	term.waitRows(t, "? Pick", "| > xxxx", "|   yyyy", "| ^v move...")
	// the closest match arrives last: it is ranked first, and without any
	// navigation the selection follows the ranking
	items <- "bat"
	term.waitRows(t, "? Pick", "| > bat", "|   xxxx", "|   yyyy", "| ^v move...")
	close(items)
	read <- keyEnter
	assert.Equal(t, "bat", answer(t, res))
}

func TestDropdownLazyDefaultKeepsNavigatedSelection(t *testing.T) {
	term := newASCIITerminal(t)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", feed(items), tui.WithDefault("cat"))
	})
	items <- "xxxx"
	items <- "yyyy"
	term.waitRows(t, "? Pick", "| > xxxx", "|   yyyy", "| ^v move...")
	term.press(t, keyDown)
	term.waitRows(t, "? Pick", "|   xxxx", "| > yyyy", "| ^v move...")
	read := term.blockedRead(t)
	// once the user has moved, a better-ranked arrival does not take the selection
	items <- "bat"
	term.waitRows(t, "? Pick", "|   bat", "|   xxxx", "| > yyyy", "| ^v move...")
	close(items)
	read <- keyEnter
	assert.Equal(t, "yyyy", answer(t, res))
}

func TestDropdownLazyFilterAppliesToLaterItems(t *testing.T) {
	term := newASCIITerminal(t)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", feed(items))
	})
	items <- "alpha"
	items <- "beta"
	term.waitRows(t, "? Pick", "| > alpha", "|   beta", "| ^v move...")
	term.press(t, "a")
	term.waitRows(t, "? Pick", "| > alpha", "| ^v move...")
	items <- "banana"
	items <- "apricot"
	term.waitRows(t, "? Pick", "| > alpha", "|   apricot", "| ^v move...")
	term.press(t, keyBackspace)
	term.waitRows(t, "? Pick", "| > alpha", "|   beta", "|   banana", "|   apricot", "| ^v move...")
	term.press(t, "a")
	term.waitRows(t, "? Pick", "| > alpha", "|   apricot", "| ^v move...")
	term.press(t, keyDown)
	term.waitRows(t, "? Pick", "|   alpha", "| > apricot", "| ^v move...")
	close(items)
	term.press(t, keyEnter)
	assert.Equal(t, "apricot", answer(t, res))
}

// testResizeWhileReading checks that resizing during a blocked read preserves selection and pending input.
func testResizeWhileReading(t *testing.T, run func() (string, error)) {
	t.Helper()
	term := newASCIITerminal(t)
	res := prompt(run)
	term.waitRows(t, "? Pick", "| > a", "|   b", "|   c", "|   d", "|   e", "| ^v move...")
	term.press(t, keyDown)
	term.waitRows(t, "? Pick", "|   a", "| > b", "|   c", "|   d", "|   e", "| ^v move...")
	term.press(t, keyDown)
	term.waitRows(t, "? Pick", "|   a", "|   b", "| > c", "|   d", "|   e", "| ^v move...")
	read := term.blockedRead(t)
	term.resize(t, 40, 5) // room for two items and the count
	term.waitRows(t, "? Pick", "|   b", "| > c", "| 2 more · ^v move · type filter")
	// a second reader would take this key and leave the prompt waiting
	read <- keyEnter
	assert.Equal(t, "c", answer(t, res))
}

func TestDropdownResizeWhileReadingKeepsSelection(t *testing.T) {
	testResizeWhileReading(t, func() (string, error) {
		return tui.Dropdown(t.Context(), "Pick", []string{"a", "b", "c", "d", "e"})
	})
}

func TestDropdownLazyResizeWhileReadingKeepsSelection(t *testing.T) {
	testResizeWhileReading(t, func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", values("a", "b", "c", "d", "e"))
	})
}
