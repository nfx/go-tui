// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"sort"
	"strings"
)

func (d *dropdown) sortRelevantByLevenstein(match string) {
	d.rankByLevenstein(d.relevant, match)
}

// maxRankRunes bounds the per-label work of similarity ranking: only the
// first maxRankRunes runes of a label and of the hint are compared, so one
// comparison costs at most maxRankRunes² steps and linear memory.
const maxRankRunes = 256

// rankByLevenstein ranks by hint similarity, breaking ties by input order.
// Cancellation leaves the current order unchanged.
func (d *dropdown) rankByLevenstein(relevant []int, match string) {
	want := []rune(strings.ToLower(match))
	scores := make(map[int]int, len(relevant))
	for _, i := range relevant {
		if d.Ctx.Err() != nil {
			return
		}
		scores[i] = choiceScore(d.rankLabel(i), want)
	}
	sort.SliceStable(relevant, func(i, j int) bool {
		a, b := relevant[i], relevant[j]
		if scores[a] == scores[b] {
			return a < b
		}
		return scores[a] < scores[b]
	})
}

// rankLabel is the text of item i that ranking compares: its projected content.
func (d *dropdown) rankLabel(i int) string { return d.search[i] }

// choiceDistance bounds Unicode edit-distance work for interactive ranking.
func choiceDistance(a, b []rune) int {
	a = a[:min(len(a), maxRankRunes)]
	b = b[:min(len(b), maxRankRunes)]
	if len(b) > len(a) {
		a, b = b, a // rows are as wide as the shorter sequence
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// levenstein keeps dropdown ranking on the shared choice-distance contract.
func (*dropdown) levenstein(a, b []rune) int { return choiceDistance(a, b) }

// choiceScore compares labels case-insensitively with an already normalized hint.
func choiceScore(label string, hint []rune) int {
	return choiceDistance([]rune(strings.ToLower(label)), hint)
}
