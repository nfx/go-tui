// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"io"
	"iter"
	"os"
	"sync"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

// syncBuffer is a bytes.Buffer safe for one writer goroutine and concurrent readers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fdByteReader struct {
	f *os.File
}

// Read returns single bytes so ReadRune sees one key at a time.
func (r *fdByteReader) Read(p []byte) (int, error) {
	buf := make([]byte, 1)
	n, err := r.f.Read(buf)
	if n > 0 {
		p[0] = buf[0]
		return 1, nil
	}
	return n, err
}

func (r *fdByteReader) Fd() uintptr {
	return r.f.Fd()
}

func startChanIO(ctx context.Context, width, height int) *chanIO {
	cio := newUnstartedIO(ctx, width, height, 0)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, io.Discard)
	return cio
}

func newTestTermIO(width, height int) *termIO {
	return &termIO{
		in:      bytes.NewBuffer(nil),
		out:     &bytes.Buffer{},
		Width:   width,
		Height:  height,
		Restore: func() error { return nil },
	}
}

type chunkReader struct {
	chunks [][]byte
	idx    int
}

// Read returns the next chunk to simulate multi-byte key sequences.
func (r *chunkReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.idx]
	r.idx++
	n := copy(p, chunk)
	return n, nil
}

// failReader fails the test when a prompt reads from it.
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Helper()
	r.t.Error("automatic answer must not read input")
	return 0, io.EOF
}

// quiet configures a prompt without terminal: no input may be read, and the
// buffer must stay empty.
func quiet(t *testing.T) opt {
	t.Helper()
	strictOutput(t)
	out := &bytes.Buffer{}
	t.Cleanup(func() { assert.Equal(t, "", out.String()) })
	return WithOptions(WithInput(failReader{t}), WithOutput(out))
}

type server struct {
	Name string
	Port int
}

type tagged struct {
	Name string
	ID   int
}

// seq yields values and records whether it ran.
func seq[T any](started *bool, err error, values ...T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		*started = true
		for _, v := range values {
			if !yield(v, nil) {
				return
			}
		}
		if err != nil {
			var zero T
			yield(zero, err)
		}
	}
}
