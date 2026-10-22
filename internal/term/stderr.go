// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"io"
)

type TerminalStderr struct {
	Parent *Terminal
}

func (w *TerminalStderr) Write(p []byte) (n int, err error) {
	if w == nil || w.Parent == nil {
		return 0, io.ErrClosedPipe
	}
	return w.Parent.writeExternal(p)
}

// Close flushes and shuts down the terminal arbiter by delegating to the parent tio.
func (w *TerminalStderr) Close() error {
	if w == nil || w.Parent == nil {
		return nil
	}
	return w.Parent.Close()
}

// Sync flushes all buffered terminal output to the underlying writer without closing.
// Satisfies the same interface as os.File.Sync so callers can drain TUI
// output without closing the descriptor.
func (w *TerminalStderr) Sync() error {
	if w == nil || w.Parent == nil {
		return nil
	}
	return w.Parent.Flush()
}

// Fd returns the file descriptor of
// the underlying terminal writer.
func (w *TerminalStderr) Fd() uintptr {
	if w == nil || w.Parent == nil {
		return 0
	}
	fdw, ok := w.Parent.RawOutput().(Descriptor)
	if !ok {
		return 0
	}
	return fdw.Fd()
}
