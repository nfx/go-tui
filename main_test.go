// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"io"
	"os"
	"testing"

	"golang.org/x/term"
)

// strictOutput restores production terminal classification for a test, where
// writers without terminal capabilities are not terminals.
func strictOutput(t *testing.T) {
	t.Helper()
	prev := outputIsTerminal
	outputIsTerminal = hostsTerminal
	t.Cleanup(func() { outputIsTerminal = prev })
}

// Most tests drive interactive widgets through in-memory writers, so the test
// binary treats them as a terminal. Tests of the production routing call
// [strictOutput]. Environment fixtures read the same on every platform.
func TestMain(m *testing.M) {
	legacyConsoles = false
	outputIsTerminal = func(w io.Writer) bool {
		if hostsTerminal(w) {
			return true
		}
		_, isFD := w.(descriptor)
		return !isFD
	}
	os.Exit(m.Run())
}

// Hooks for the external test package, see integration_test.go.

// UseTheme installs th and routes migrated widgets through it for the test,
// for the external test package.
func UseTheme(t *testing.T, th Theme) {
	t.Helper()
	useTheme(t, th)
}

// ASCIISymbols replace the presets on terminals without the Unicode glyphs.
var ASCIISymbols = asciiSymbols

// WithEnv installs an output environment fixture built from KEY=VALUE pairs.
func WithEnv(t *testing.T, pairs ...string) {
	t.Helper()
	withEnv(t, pairs...)
}

// ProbingKeyboard makes input on fd a terminal that answers background
// queries, without changing any terminal mode.
func ProbingKeyboard(t *testing.T, fd uintptr) {
	t.Helper()
	prevInput, prevSupported := terminalInputChecker, backgroundProbeSupported
	prevRaw, prevRestore := termMakeRaw, termRestore
	terminalInputChecker = func(f int) bool { return f == int(fd) || prevInput(f) }
	backgroundProbeSupported = true
	termMakeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	termRestore = func(int, *term.State) error { return nil }
	t.Cleanup(func() {
		terminalInputChecker, backgroundProbeSupported = prevInput, prevSupported
		termMakeRaw, termRestore = prevRaw, prevRestore
	})
}
