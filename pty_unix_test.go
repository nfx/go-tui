// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build darwin || linux

package tui_test

import (
	"os"
	"syscall"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"golang.org/x/sys/unix"
)

// openTTY returns the terminal side of a new pseudo-terminal of the given size.
func openTTY(t *testing.T, width, height int) *os.File {
	t.Helper()
	master, tty, err := openPty()
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() {
		tty.Close()
		master.Close()
	})
	setTTYSize(t, tty, width, height)
	return tty
}

func setTTYSize(t *testing.T, tty *os.File, width, height int) {
	t.Helper()
	assert.NoError(t, unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{
		Row: uint16(height),
		Col: uint16(width),
	}))
}

// resizeTTY changes the terminal size and signals the process like the
// terminal emulator does for its foreground process.
func resizeTTY(t *testing.T, tty *os.File, width, height int) {
	t.Helper()
	setTTYSize(t, tty, width, height)
	assert.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGWINCH))
}
