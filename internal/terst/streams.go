// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"io"
	"os"
	"sync"
	"testing"
)

type FDByteReader struct {
	F *os.File
}

// Read returns single bytes so ReadRune sees one key at a time.
func (r *FDByteReader) Read(p []byte) (int, error) {
	buf := make([]byte, 1)
	n, err := r.F.Read(buf)
	if n > 0 {
		p[0] = buf[0]
		return 1, nil
	}
	return n, err
}

func (r *FDByteReader) Fd() uintptr {
	return r.F.Fd()
}

// failReader fails the test when a prompt reads from it.
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Helper()
	r.t.Error("automatic answer must not read input")
	return 0, io.EOF
}

type FailingWriter struct{ Err error }

func (w FailingWriter) Write([]byte) (int, error) { return 0, w.Err }

type MockDescriptor struct {
	io.Reader
	io.Writer
	FD uintptr
}

func (m *MockDescriptor) Fd() uintptr {
	return m.FD
}

// NotifyingReader exposes a descriptor while reporting when a blocking read starts.
type NotifyingReader struct {
	*os.File
	Started chan struct{}
	once    sync.Once
}

// NewNotifyingReader wraps f.
func NewNotifyingReader(f *os.File) *NotifyingReader {
	return &NotifyingReader{File: f, Started: make(chan struct{})}
}

// Read signals entry before reading one byte, like a wrapper that limits chunks.
func (r *NotifyingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.Started) })
	return r.File.Read(p[:1])
}
