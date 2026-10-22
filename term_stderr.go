// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"io"
)

type terminalStderr struct {
	parent *tio
}

func (w *terminalStderr) Write(p []byte) (n int, err error) {
	if w == nil || w.parent == nil {
		return 0, io.ErrClosedPipe
	}
	return w.parent.writeExternal(p)
}

// Close flushes and shuts down the terminal arbiter by delegating to the parent tio.
func (w *terminalStderr) Close() error {
	if w == nil || w.parent == nil {
		return nil
	}
	return w.parent.Close()
}

// Sync flushes all buffered terminal output to the underlying writer without closing.
// Satisfies the same interface as os.File.Sync so callers can drain TUI
// output without closing the descriptor.
func (w *terminalStderr) Sync() error {
	if w == nil || w.parent == nil {
		return nil
	}
	return w.parent.Flush()
}

// Fd returns the file descriptor of
// the underlying terminal writer.
func (w *terminalStderr) Fd() uintptr {
	if w == nil || w.parent == nil {
		return 0
	}
	fdw, ok := w.parent.rawOutput().(descriptor)
	if !ok {
		return 0
	}
	return fdw.Fd()
}
