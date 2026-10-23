// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"io"
	"testing"
)

// failReader fails the test when a prompt reads from it.
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Helper()
	r.t.Error("automatic answer must not read input")
	return 0, io.EOF
}

// FailingWriter fails every write with Err.
type FailingWriter struct{ Err error }

func (w FailingWriter) Write([]byte) (int, error) { return 0, w.Err }

// keyboard is input with a descriptor, which widgets read as a terminal.
type keyboard struct{ io.Reader }

func (keyboard) Fd() uintptr { return 0 }
