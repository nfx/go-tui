// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"io"
	"os"
	"testing"
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
// [strictOutput].
func TestMain(m *testing.M) {
	outputIsTerminal = func(w io.Writer) bool {
		if hostsTerminal(w) {
			return true
		}
		_, isFD := w.(descriptor)
		return !isFD
	}
	os.Exit(m.Run())
}
