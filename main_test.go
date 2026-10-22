// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"
	"io"
	"os"
	"testing"

	xterm "golang.org/x/term"
)

// strictOutput restores production terminal classification for a test, where
// writers without terminal capabilities are not terminals.
func strictOutput(t *testing.T) {
	t.Helper()
	prev := term.OutputIsTerminal
	term.OutputIsTerminal = term.HostsTerminal
	t.Cleanup(func() { term.OutputIsTerminal = prev })
}

// Most tests drive interactive widgets through in-memory writers, so the test
// binary treats them as a terminal. Tests of the production routing call
// [strictOutput]. Environment fixtures read the same on every platform, and
// the suite drives terminals the same in a CI job.
func TestMain(m *testing.M) {
	term.LegacyConsoles = false
	term.Default.Env.Ci = false
	term.OutputIsTerminal = func(w io.Writer) bool {
		if term.HostsTerminal(w) {
			return true
		}
		_, isFD := w.(term.Descriptor)
		return !isFD
	}
	os.Exit(m.Run())
}

// Hooks for the external test package, see integration_test.go.

// UseTheme installs th and routes migrated widgets through it for the test,
// for the external test package.
func UseTheme(t *testing.T, th theme.Theme) {
	t.Helper()
	useTheme(t, th)
}

// ASCIISymbols replace the presets on terminals without the Unicode glyphs.
var ASCIISymbols = theme.New().Symbols.ASCII()

// WithEnv installs an output environment fixture built from KEY=VALUE pairs.
func WithEnv(t *testing.T, pairs ...string) {
	t.Helper()
	withEnv(t, pairs...)
}

// ProbingKeyboard makes input on fd a terminal that answers background
// queries, without changing any terminal mode.
func ProbingKeyboard(t *testing.T, fd uintptr) {
	t.Helper()
	prevInput, prevSupported := term.InputChecker, term.ProbeSupported
	prevRaw, prevRestore := term.MakeRaw, term.Restore
	term.InputChecker = func(f int) bool { return f == int(fd) || prevInput(f) }
	term.ProbeSupported = true
	term.MakeRaw = func(int) (*xterm.State, error) { return &xterm.State{}, nil }
	term.Restore = func(int, *xterm.State) error { return nil }
	t.Cleanup(func() {
		term.InputChecker, term.ProbeSupported = prevInput, prevSupported
		term.MakeRaw, term.Restore = prevRaw, prevRestore
	})
}
