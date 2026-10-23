// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"fmt"
	"iter"
	"regexp"
	"strings"
	"testing"
	"time"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/assert"
)

func TestWidgetsShareTerminal(t *testing.T) {
	term := newASCIITerminal(t)
	p, err := tui.NewMaxProgressBar(t.Context(), "Download", 4)
	assert.NoError(t, err)
	p.Add(1)
	term.AwaitVisible("Download  25%  ...", "1 / 4 items...")

	res := prompt(func() (string, error) {
		return tui.Dropdown(t.Context(), "Pick", []string{"a", "b"})
	})
	// the background overlay stays above the prompt that started later
	term.AwaitVisible("Download  25%  ...", "1 / 4 items...", "? Pick", "| > a", "|   b", "| ^v move...")

	// log output goes above both overlays
	_, err = fmt.Fprintln(tui.Stderr(), "log line")
	assert.NoError(t, err)
	term.AwaitVisible("log line", "Download  25%  ...", "1 / 4 items...", "? Pick", "| > a", "|   b", "| ^v move...")

	term.press(t, keyEnter)
	assert.Equal(t, "a", answer(t, res))
	term.AwaitVisible("log line", "+ Pick · a", "Download  25%  ...", "1 / 4 items...")

	p.Add(3) // completes the bar, which then disappears
	term.AwaitVisible("log line", "+ Pick · a")
	assert.NoError(t, p.Close())

	// the next prompt reads from the same keyboard and draws on the same screen
	res = prompt(func() (string, error) {
		return tui.Input(t.Context(), "Name")
	})
	term.press(t, "x")
	term.press(t, keyEnter)
	assert.Equal(t, "x", answer(t, res))
	term.AwaitVisible("log line", "+ Pick · a", "+ Name · x")
}

type scoredRow struct {
	Name  string
	Score int
}

var scoreCell = regexp.MustCompile(`(\x1b\[[0-9;]*m)?(\d+) *(?:\x1b\[0m)? *$`)

// scoreColors maps every score in a rendered table to the color of its cell.
func scoreColors(t *testing.T, rows []scoredRow) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	assert.NoError(t, tui.TableAuto(&buf, rows, tui.WithColumnGreenRedScale("Score")))
	colors := map[string]string{}
	for line := range strings.SplitSeq(buf.String(), "\n") {
		m := scoreCell.FindStringSubmatch(line)
		if m != nil {
			colors[m[2]] = m[1]
		}
		// an empty cell is never colored
		assert.NotContains(t, line, "m\x1b[0m")
	}
	assert.Equal(t, len(rows), len(colors))
	return colors
}

func TestTableAutoScaleColorsFollowMultilineRows(t *testing.T) {
	scores := []int{7, 3, 11, 1, 9, 5, 12, 2, 8, 4, 10, 6}
	var single, multiline []scoredRow
	for i, score := range scores {
		name := strings.Repeat(string(rune('a'+i)), 3)
		single = append(single, scoredRow{name, score})
		if i == 3 || i == 10 { // in both the first and the second batch
			name += "\ncontinued"
		}
		multiline = append(multiline, scoredRow{name, score})
	}
	// a value with a newline does not change which color any score gets
	assert.Equal(t, scoreColors(t, single), scoreColors(t, multiline))
}

type promptResult struct {
	value string
	err   error
}

// prompt runs fn in the background, like a CLI waiting on its user.
func prompt(fn func() (string, error)) <-chan promptResult {
	res := make(chan promptResult, 1)
	go func() {
		value, err := fn()
		res <- promptResult{value, err}
	}()
	return res
}

func answer(t *testing.T, res <-chan promptResult) string {
	t.Helper()
	select {
	case r := <-res:
		assert.NoError(t, r.err)
		return r.value
	case <-time.After(failsafe):
		t.Fatal("prompt did not return")
		return ""
	}
}

// feed returns an iterator that yields the items the test sends on ch.
func feed(ch <-chan string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for v := range ch {
			if !yield(v, nil) {
				return
			}
		}
	}
}

func values(items ...string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for _, v := range items {
			if !yield(v, nil) {
				return
			}
		}
	}
}
