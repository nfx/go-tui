// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"testing"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"
)

// asciiSymbols replace the presets on terminals without the Unicode glyphs.
var asciiSymbols = theme.New().Symbols.ASCII()

// StripSGR removes SGR sequences, keeping text and every other sequence.
func StripSGR(s string) string {
	var out ansi.Text
	for seg := range ansi.Text(s).Segments() {
		if _, sgr := seg.SGRParams(); seg.IsEscape && sgr {
			continue
		}
		out = append(out, seg.Text...)
	}
	return out.String()
}

// ASCIITheme is the preset theme with ASCII symbols.
func ASCIITheme() theme.Theme {
	th := theme.New()
	th.Symbols = asciiSymbols
	return th
}

// UseTheme installs th for the test and restores the installed theme after it.
func UseTheme(t *testing.T, th theme.Theme) {
	t.Helper()
	prev := term.Default.CurrentTheme()
	t.Cleanup(func() { term.Default.Theme.Store(prev) })
	assert.NoError(t, term.Default.InstallTheme(th))
}

// UseASCII installs [ASCIITheme] for the test.
func UseASCII(t *testing.T) {
	t.Helper()
	UseTheme(t, ASCIITheme())
}
