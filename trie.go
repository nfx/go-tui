// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type trie struct {
	m   map[rune]*trie
	idx []int
}

func newTrie() *trie {
	return &trie{
		m: map[rune]*trie{},
	}
}

// Add indexes the words in word, ignoring terminal controls and escapes.
//
//nolint:cyclop // it's ok
func (t *trie) Add(word text, i int) {
	r := t
	for seg := range word.segments() {
		if !seg.isText() {
			continue // escape sequences with their payloads, and controls
		}
		for rest := seg.text; len(rest) > 0; {
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
				s = newTrie()
				r.m[b] = s
			}
			r = s
		}
	}
	r.idx = append(r.idx, i)
}

// Prefix returns sorted indexes of items where every word of the prefix
// is a prefix of some word of the item.
func (t *trie) Prefix(prefix string) []int {
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
func (t *trie) queryWords(query string) (words []string) {
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

func (t *trie) wordPrefix(word string) []int {
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
func (t *trie) intersect(a, b []int) (out []int) {
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

func (t *trie) Words() (out []string) {
	words := t.dfs("")
	sort.Strings(words)
	return words
}

func (t *trie) Indexes() (out []int) {
	q := []*trie{t}
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

func (t *trie) dfs(s string) []string {
	var out []string
	if len(t.idx) > 0 {
		out = append(out, s)
	}
	for k, v := range t.m {
		out = append(out, v.dfs(s+string(k))...)
	}
	return out
}
