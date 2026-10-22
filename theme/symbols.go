// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package theme

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"slices"
	"unicode"
)

// Symbols are the glyphs the built-in templates and renderers use. Every
// symbol is a single display column. An empty field takes the Unicode preset.
type Symbols struct { //nolint:recvcheck // fields hands out pointers into its receiver
	Prompt    string
	Focus     string
	Guide     string
	End       string
	Checked   string
	Unchecked string
	Success   string
	Error     string
	Cancel    string
	Ellipsis  string
	Mask      string
	// Up and Down name the arrow keys in key hints.
	Up   string
	Down string
	// BarFill draws the done cells of a progress bar, BarHead the partly
	// done cell after them, and BarEmpty the rest of its track.
	BarFill  string
	BarHead  string
	BarEmpty string
	// Unknown stands for a value that is not known, such as the completion
	// of a progress bar without a total.
	Unknown string
	// Separator separates the entries of a progress hint.
	Separator string
	// Spinner are the frames of a running spinner, in order. Each frame is
	// a symbol, so it is a single display column like the others. Empty
	// takes the preset.
	Spinner []string
}

// unicodeSymbols is the preset every empty symbol falls back to.
var unicodeSymbols = Symbols{
	Prompt:    "◆",
	Focus:     "❯",
	Guide:     "│",
	End:       "└",
	Checked:   "◉",
	Unchecked: "○",
	Success:   "✔",
	Error:     "✖",
	Cancel:    "■",
	Ellipsis:  "…",
	Mask:      "*",
	Up:        "↑",
	Down:      "↓",
	BarFill:   "━",
	BarHead:   "╸",
	BarEmpty:  "─",
	Unknown:   "—",
	Separator: "·",
	Spinner:   []string{"▘", "▀", "▝", "▐", "▗", "▄", "▖", "▌"},
}

// asciiSymbols replace the presets on terminals whose fonts likely lack the
// Unicode glyphs, see [outputEnv.hasUnicode].
var asciiSymbols = Symbols{
	Prompt:    "?",
	Focus:     ">",
	Guide:     "|",
	End:       "`",
	Checked:   "x",
	Unchecked: "o",
	Success:   "+",
	Error:     "!",
	Cancel:    "-",
	Ellipsis:  ".",
	Mask:      "*",
	Up:        "^",
	Down:      "v",
	BarFill:   "=",
	BarHead:   ">",
	BarEmpty:  "-",
	Unknown:   "?",
	Separator: "|",
	// the line spinner of cli-spinners (MIT License)
	Spinner: []string{"|", "/", "-", `\`},
}

// clone copies the symbols, so that edits never reach the preset.
func (s Symbols) clone() Symbols {
	s.Spinner = slices.Clone(s.Spinner)
	return s
}

// symbolField is one single-glyph field of [Symbols].
type symbolField struct {
	name  string
	glyph *string
}

// fields lists the single-glyph fields of s in declaration order.
func (s *Symbols) fields() []symbolField {
	return []symbolField{
		{"Prompt", &s.Prompt},
		{"Focus", &s.Focus},
		{"Guide", &s.Guide},
		{"End", &s.End},
		{"Checked", &s.Checked},
		{"Unchecked", &s.Unchecked},
		{"Success", &s.Success},
		{"Error", &s.Error},
		{"Cancel", &s.Cancel},
		{"Ellipsis", &s.Ellipsis},
		{"Mask", &s.Mask},
		{"Up", &s.Up},
		{"Down", &s.Down},
		{"BarFill", &s.BarFill},
		{"BarHead", &s.BarHead},
		{"BarEmpty", &s.BarEmpty},
		{"Unknown", &s.Unknown},
		{"Separator", &s.Separator},
	}
}

func (s Symbols) normalize() (Symbols, error) {
	def := unicodeSymbols
	defs := def.fields()
	for i, f := range s.fields() {
		if *f.glyph == "" {
			*f.glyph = *defs[i].glyph
		}
		err := s.singleColumn(*f.glyph)
		if err != nil {
			return s, fmt.Errorf("theme.Symbols.%s: %w", f.name, err)
		}
	}
	if len(s.Spinner) == 0 {
		s.Spinner = def.Spinner
	}
	s.Spinner = slices.Clone(s.Spinner)
	for i, frame := range s.Spinner {
		err := s.singleColumn(frame)
		if err != nil {
			return s, fmt.Errorf("theme.Symbols.Spinner[%d]: %w", i, err)
		}
	}
	return s, nil
}

// ASCII replaces every Unicode preset symbol of s with its counterpart in
// [asciiSymbols], and keeps the symbols the theme chose itself.
func (s Symbols) ASCII() Symbols {
	s = s.clone()
	def, alt := unicodeSymbols, asciiSymbols.clone()
	defs, alts := def.fields(), alt.fields()
	for i, f := range s.fields() {
		if *f.glyph == *defs[i].glyph {
			*f.glyph = *alts[i].glyph
		}
	}
	if slices.Equal(s.Spinner, def.Spinner) {
		s.Spinner = alt.Spinner
	}
	return s
}

// singleColumn checks a glyph that must occupy exactly one display column.
func (Symbols) singleColumn(s string) error {
	if s == "" {
		return errors.New("must not be empty")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("%q: control character", s)
		}
	}
	w := ansi.Text(s).Width()
	if w != 1 || len(ansi.Text(s).Clusters()) != 1 {
		return fmt.Errorf("%q: want one display column, got %d", s, w)
	}
	return nil
}

// Named returns the glyph of the field called name, such as "Prompt" or "Up".
// It is the lookup behind the symbol template helper.
func (s Symbols) Named(name string) (string, error) {
	for _, f := range s.fields() {
		if f.name == name {
			return *f.glyph, nil
		}
	}
	return "", fmt.Errorf("symbol %q: no such symbol", name)
}

// Arrows names the up and down arrow keys in key hints.
func (s Symbols) Arrows() ansi.Text { return ansi.Text(s.Up + s.Down) }
