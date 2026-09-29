// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"slices"
	"sort"
	"unicode"
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

//nolint:cyclop // it's ok
func (t *trie) Add(word string, i int) {
	r := t
	var escape, isLetter bool
	for _, b := range word {
		if escape && isEscapeEnd(byte(b)) {
			escape = false
			continue
		} else if isEscapeStart(byte(b)) {
			escape = true
		}
		if escape {
			continue
		}
		if b == ' ' {
			if r != t {
				r.idx = append(r.idx, i)
			}
			r = t
			continue
		}
		isLetter = unicode.IsLetter(b)
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
	r.idx = append(r.idx, i)
}

func (t *trie) Prefix(prefix string) []int {
	r := t
	var isLetter bool
	for _, b := range prefix {
		isLetter = unicode.IsLetter(b)
		if !isLetter && !unicode.IsDigit(b) {
			if len(r.m) == 0 && len(r.Indexes()) > 0 {
				// if one full word matched, we're good
				break
			}
			continue
		}
		if isLetter {
			b = unicode.ToLower(b)
		}
		s, ok := r.m[b]
		if !ok {
			return nil
		}
		r = s
	}
	out := r.Indexes()
	slices.Sort(out)
	return slices.Compact(out)
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
