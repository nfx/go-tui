// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package trie

import (
	"github.com/nfx/go-tui/internal/ansi"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Trie struct {
	m   map[rune]*Trie
	idx []int
}

func NewTrie() *Trie {
	return &Trie{
		m: map[rune]*Trie{},
	}
}

// Add indexes the words in word, ignoring terminal controls and escapes.
func (t *Trie) Add(word ansi.Text, i int) {
	r := t
	for seg := range word.Segments() {
		if !seg.IsText() {
			continue // escape sequences with their payloads, and controls
		}
		for rest := seg.Text; len(rest) > 0; {
			b, size := utf8.DecodeRune(rest)
			rest = rest[size:]
			if b == ' ' {
				if r != t {
					r.idx = append(r.idx, i)
				}
				r = t
				continue
			}
			isLetter := unicode.IsLetter(b)
			if !isLetter && !unicode.IsDigit(b) {
				continue
			}
			if isLetter {
				b = unicode.ToLower(b)
			}
			s, ok := r.m[b]
			if !ok {
				s = NewTrie()
				r.m[b] = s
			}
			r = s
		}
	}
	r.idx = append(r.idx, i)
}

// Prefix returns sorted indexes of items where every word of the prefix
// is a prefix of some word of the item.
func (t *Trie) Prefix(prefix string) []int {
	var out []int
	for i, word := range t.queryWords(prefix) {
		found := t.wordPrefix(word)
		if i == 0 {
			out = found
		} else {
			out = t.intersect(out, found)
		}
		if len(out) == 0 {
			return nil
		}
	}
	if out == nil {
		// prefix without letters or digits matches everything
		out = t.Indexes()
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// queryWords tokenizes the query the same way Add does: words are separated
// by spaces, and other non-alphanumeric runes are dropped within a word.
func (t *Trie) queryWords(query string) (words []string) {
	for field := range strings.SplitSeq(query, " ") {
		word := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, field)
		if word != "" {
			words = append(words, word)
		}
	}
	return words
}

func (t *Trie) wordPrefix(word string) []int {
	r := t
	for _, b := range word {
		s, ok := r.m[unicode.ToLower(b)]
		if !ok {
			return nil
		}
		r = s
	}
	out := r.Indexes()
	slices.Sort(out)
	return slices.Compact(out)
}

// intersect returns common elements of two sorted slices in linear time.
func (t *Trie) intersect(a, b []int) (out []int) {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return out
}

func (t *Trie) Words() (out []string) {
	words := t.dfs("")
	sort.Strings(words)
	return words
}

func (t *Trie) Indexes() (out []int) {
	q := []*Trie{t}
	for len(q) > 0 {
		r := q[0]
		q = q[1:]
		out = append(out, r.idx...)
		for _, s := range r.m {
			q = append(q, s)
		}
	}
	// keep output in the same order
	sort.Ints(out)
	return
}

func (t *Trie) dfs(s string) []string {
	var out []string
	if len(t.idx) > 0 {
		out = append(out, s)
	}
	for k, v := range t.m {
		out = append(out, v.dfs(s+string(k))...)
	}
	return out
}

// HintFromTrie looks up a word-prefix hint. A hint without letters or digits
// would match everything, so it matches nothing.
func HintFromTrie(t *Trie) func(string) ([]int, error) {
	return func(s string) ([]int, error) {
		if len(t.queryWords(s)) == 0 {
			return nil, nil
		}
		return t.Prefix(s), nil
	}
}
