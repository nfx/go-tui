// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/nfx/go-tui/terst"
)

// ttyWriter is the terminal output: it is a real terminal to check and to
// measure, but each write goes to the screen in one piece.
type ttyWriter struct {
	fd  uintptr
	out *terst.Output
}

func (w *ttyWriter) Write(p []byte) (int, error) {
	return w.out.Write(p)
}

func (w *ttyWriter) Fd() uintptr {
	return w.fd
}

type terminal struct {
	*keyboard
	*terst.Output
	tty *os.File
}

// newTerminal makes a terminal of the given size the package default for
// input and output, like the stdin and stderr of a CLI.
func newTerminal(t *testing.T, height int) *terminal {
	t.Helper()
	const width = 40
	tty := openTTY(t, width, height)
	term := &terminal{keyboard: newKeyboard(t), Output: terst.NewOutput(t), tty: tty}
	// rows below the bottom stay on the screen, as in the scrollback
	term.Resize(width, 0)
	terst.UseIO(t, term.keyboard, &ttyWriter{fd: tty.Fd(), out: term.Output})
	return term
}

func (term *terminal) resize(t *testing.T, width, height int) {
	t.Helper()
	resizeTTY(t, term.tty, width, height)
}

// newASCIITerminal is newTerminal with the ASCII theme: the active row is
// marked with ">" and the answer line starts with "+".
func newASCIITerminal(t *testing.T) *terminal {
	t.Helper()
	terst.UseASCII(t)
	return newTerminal(t, 12)
}

// waitQueries waits until the terminal has received n background queries.
// A probe may block on the keyboard before it writes its query.
func (term *terminal) waitQueries(t *testing.T, n int) {
	t.Helper()
	for strings.Count(term.String(), backgroundQuery) < n {
		term.AwaitOutput(backgroundQuery)
	}
}

// answerProbe replies to the nth background query, sent by a prompt.
func (term *terminal) answerProbe(t *testing.T, n int, reply string) {
	t.Helper()
	read := term.blockedRead(t)
	term.waitQueries(t, n)
	read <- reply
}
