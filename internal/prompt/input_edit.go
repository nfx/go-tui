// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/term"
)

// visibleWindow returns the start and end indexes of the clusters
// that fit into availW terminal columns, anchored around the cursor.
func (i *input) visibleWindow(cells ansi.Clusters, availW int) (int, int) {
	at := cells.At(i.cursor)
	if availW <= 0 {
		return at, at
	}
	if cells.Width() <= availW {
		return 0, len(cells)
	}
	half := availW / 2
	start := 0
	switch {
	case cells[:at].Width() <= half:
	case cells[at:].Width() <= half:
		// fill the window backwards from the end of the text
		start = len(cells)
		for w := 0; start > 0 && w+cells[start-1].Width <= availW; start-- {
			w += cells[start-1].Width
		}
	default:
		// keep half of the window before the cursor
		start = at
		for w := 0; start > 0 && w+cells[start-1].Width <= half; start-- {
			w += cells[start-1].Width
		}
	}
	end := start
	for w := 0; end < len(cells) && w+cells[end].Width <= availW; end++ {
		w += cells[end].Width
	}
	return start, end
}

// prevBoundary returns the rune offset of the cluster boundary before cursor.
func (i *input) prevBoundary(cursor int) int {
	cells := ansi.Text(string(i.displayRunes())).Clusters()
	if k := cells.At(cursor - 1); k < len(cells) {
		return cells[k].Start
	}
	return max(cursor-1, 0)
}

// nextBoundary returns the rune offset of the cluster boundary after cursor.
func (i *input) nextBoundary(cursor int) int {
	cells := ansi.Text(string(i.displayRunes())).Clusters()
	if k := cells.At(cursor); k < len(cells) {
		return cells[k].End
	}
	return cursor + 1
}

// displayRunes returns one terminal-safe rune per typed rune, so that
// the cursor indexes both: a password is masked, and control characters,
// which a paste stores as text, are shown instead of being interpreted.
func (i *input) displayRunes() []rune {
	runes := []rune(i.typed)
	for k, r := range runes {
		if i.Password {
			runes[k] = '*'
		} else {
			runes[k] = i.safeRune(r)
		}
	}
	return runes
}

// safeRune replaces a control character with its Unicode control picture,
// or with the replacement character for C1 controls, which have no picture.
func (*input) safeRune(r rune) rune {
	switch {
	case r < 0x20:
		return 0x2400 + r // e.g. ␍ for a carriage return
	case r == 0x7f:
		return '\u2421' // ␡
	case unicode.IsControl(r):
		return utf8.RuneError
	default:
		return r
	}
}

// safeText applies [input.safeRune] to every rune of text.
func (i *input) safeText(text string) string {
	return strings.Map(i.safeRune, text)
}

// clear erases the input and any feedback row before the prompt exits.
func (i *input) clear(io *term.IO) error {
	var errs []error
	rows := max(i.rows, 1)
	down := rows
	if io.Vp == nil {
		// from the caret row to below the frame; a viewport replaces the frame
		down -= i.caretRow
	}
	_, err := fmt.Fprint(io, strings.Repeat("\n", down))
	if err != nil {
		errs = append(errs, fmt.Errorf("newline: %w", err))
	}
	err = io.Clear(rows, io)
	if err != nil {
		errs = append(errs, fmt.Errorf("clear: %w", err))
	}
	return errors.Join(errs...)
}

// insertAtCursor returns a change that inserts text at the cursor,
// which counts runes rather than bytes, and moves the cursor after it.
func (p *input) insertAtCursor(text string) inputChanged {
	runes := []rune(p.typed)
	inserted := []rune(text)
	cursor := min(p.cursor, len(runes))
	next := cursor + len(inserted)
	return inputChanged{
		Text:   string(runes[:cursor]) + string(inserted) + string(runes[cursor:]),
		Cursor: &next,
	}
}

// pressLeft moves the cursor to the preceding cluster boundary.
func (p *input) pressLeft() {
	if p.cursor > 0 {
		p.cursor = p.prevBoundary(p.cursor)
	}
}

// pressRight moves the cursor to the next cluster boundary.
func (p *input) pressRight() {
	if p.cursor < utf8.RuneCountInString(p.typed) {
		p.cursor = p.nextBoundary(p.cursor)
	}
}

func (p *input) pressAny(key rune) {
	p.applyInputChanged(p.insertAtCursor(string(key)))
}
