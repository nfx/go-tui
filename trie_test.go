// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestTrieEscapes(t *testing.T) {
	trie := newTrie()
	trie.Add(text("\x1b[31mhello\x1b[0m"), 1)
	trie.Add(text("\x1b[31mHELLO\x1b[0m wO\x1b[42mRl\x1b[0md"), 2)
	trie.Add(text("high"), 3)
	trie.Add(text("wo\x1b[42mrl\x1b[0md"), 4)
	trie.Add(text("wonderful"), 5)
	trie.Add(text("привіт"), 6)
	trie.Add(text("\x1b[31mпобут\x1b[0m"), 7)
	trie.Add(text("світ"), 7)
	trie.Add(text("сокіл"), 9)
	trie.Add(text("Н_ОВ_ИЙ    П_Р_ИВ_І_Д"), 10)

	assert.Equal(t, []int{1, 2, 3}, trie.Prefix("h"))
	assert.Equal(t, []int{6, 7, 10}, trie.Prefix("_п_"))
	assert.Equal(t, []string{
		"hello", "high", "wonderful", "world",
		"новий", "побут", "привід", "привіт", "світ", "сокіл",
	}, trie.Words())
}

func TestTriePrefixDoesNotIgnoreTrailingWords(t *testing.T) {
	trie := newTrie()
	trie.Add(text("beta"), 0)
	trie.Add(text("beta gamma"), 1)
	assert.Equal(t, []int{0, 1}, trie.Prefix("beta"))
	assert.Equal(t, []int{1}, trie.Prefix("beta gam"))
	assert.Equal(t, 0, len(trie.Prefix("beta x")))
}

func TestTriePrefixMatchesPunctuatedWord(t *testing.T) {
	trie := newTrie()
	trie.Add(text("beta-gamma"), 0)
	trie.Add(text("beta gamma"), 1)
	assert.Equal(t, []int{0}, trie.Prefix("beta-gamma"))
	assert.Equal(t, []int{0}, trie.Prefix("beta-gam"))
	assert.Equal(t, []int{1}, trie.Prefix("beta gamma"))
}

func TestIntersectSorted(t *testing.T) {
	assert.Equal(t, []int{2, 5}, new(trie).intersect([]int{1, 2, 3, 5, 8}, []int{2, 4, 5, 9}))
	assert.Equal(t, 0, len(new(trie).intersect([]int{1, 3}, []int{2, 4})))
}

func TestTrieSkipsControlStrings(t *testing.T) {
	trie := newTrie()
	trie.Add(text("\x1b]8;;http://example.com/zzz\x07link\x1b]8;;\x07 text"), 1)
	trie.Add(text("\x1b]0;title zzz\x1b\\plain"), 2)
	assert.Equal(t, []string{"link", "plain", "text"}, trie.Words())
	assert.Equal(t, []int(nil), trie.Prefix("zzz"))
}

func TestTrieWordSpansEscapes(t *testing.T) {
	trie := newTrie()
	trie.Add(text("he\x1b[1ml\x1b]8;;http://x y\x07lo\x1b]8;;\x07 wo\x1b(Brld"), 1)
	assert.Equal(t, []string{"hello", "world"}, trie.Words())
	assert.Equal(t, []int{1}, trie.Prefix("hel wor"))
	assert.Equal(t, []int(nil), trie.Prefix("http"))
}
