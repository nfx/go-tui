// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui_test

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/assert"
)

// ttyWriter is the terminal output: it is a real terminal to check and to
// measure, but each write goes to the screen in one piece.
type ttyWriter struct {
	fd     uintptr
	screen *screen
}

func (w *ttyWriter) Write(p []byte) (int, error) {
	return w.screen.Write(p)
}

func (w *ttyWriter) Fd() uintptr {
	return w.fd
}

type terminal struct {
	*keyboard
	*screen
	tty *os.File
}

// newTerminal makes a terminal of the given size the package default for
// input and output, like the stdin and stderr of a CLI.
func newTerminal(t *testing.T, height int) *terminal {
	t.Helper()
	const width = 40
	tty := openTTY(t, width, height)
	term := &terminal{keyboard: newKeyboard(t), screen: newScreen(width), tty: tty}
	tui.SetDefaultIO(term.keyboard, &ttyWriter{fd: tty.Fd(), screen: term.screen})
	t.Cleanup(func() {
		// a later terminal of the same test may have restored os.Stderr already
		closer, ok := tui.Stderr().(io.Closer)
		if ok && closer != io.Closer(os.Stderr) {
			assert.NoError(t, closer.Close())
		}
		tui.SetDefaultIO(os.Stdin, os.Stderr)
	})
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
	asciiTheme(t)
	return newTerminal(t, 12)
}

// asciiTheme installs the ASCII preset as the default theme for the test.
func asciiTheme(t *testing.T) {
	t.Helper()
	th := tui.NewTheme()
	th.Symbols = tui.ASCIISymbols
	tui.UseTheme(t, th)
}

// waitQueries waits until the terminal has received n background queries.
// A probe may block on the keyboard before it writes its query.
func (term *terminal) waitQueries(t *testing.T, n int) {
	t.Helper()
	timeout := time.After(failsafe)
	for {
		term.screen.mu.Lock()
		got, changed := strings.Count(string(term.raw), backgroundQuery), term.changed
		term.screen.mu.Unlock()
		if got == n {
			return
		}
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("timed out waiting for %d background queries, got %d", n, got)
		}
	}
}

// answerProbe replies to the nth background query, sent by a prompt.
func (term *terminal) answerProbe(t *testing.T, n int, reply string) {
	t.Helper()
	read := term.blockedRead(t)
	term.waitQueries(t, n)
	read <- reply
}
