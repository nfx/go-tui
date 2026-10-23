// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/term"
)

// Quiet makes the default IO a prompt without terminal for the test: no input
// may be read, and the output must stay empty.
func Quiet(t *testing.T) {
	t.Helper()
	StrictOutput(t)
	out := &bytes.Buffer{}
	t.Cleanup(func() { assert.Equal(t, "", out.String()) })
	UseIO(t, failReader{t}, out)
}

// UseIO makes in and out the default input and output for the test, like the
// stdin and stderr of a CLI. Cleanup closes the coordinated writer and
// restores os.Stdin and os.Stderr.
func UseIO(t *testing.T, in io.Reader, out io.Writer) {
	t.Helper()
	term.Default.SetIO(in, out)
	t.Cleanup(func() {
		// close the arbiter only: the test owns out
		assert.NoError(t, term.Default.Close())
		term.Default.SetIO(os.Stdin, os.Stderr)
	})
}

// Main patches the terminal seams for a package test binary, then runs the
// tests. Most tests drive interactive widgets through in-memory writers, so
// the binary treats them as a terminal. Tests of the production routing call
// [StrictOutput]. Environment fixtures read the same on every platform, and
// the suite drives terminals the same in a CI job.
func Main(m *testing.M) {
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
