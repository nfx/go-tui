// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/term"
)

// Key sequences as a terminal sends them.
const (
	KeyEnter     = "\r"
	KeyEsc       = "\x1b"
	KeyTab       = "\t"
	KeyBackspace = "\x7f"
	KeyUp        = "\x1b[A"
	KeyDown      = "\x1b[B"
	KeyRight     = "\x1b[C"
	KeyLeft      = "\x1b[D"
)

// Keys is scripted keyboard input: Read returns one chunk, blocking until one is sent.
type Keys struct {
	t       *testing.T
	ch      chan []byte
	reads   chan struct{}
	waiters atomic.Int32
	closed  sync.Once
}

// NewKeys returns input that serves chunks, one per Read, and then blocks until
// more are sent. It stays open until [Keys.Close] or the end of the test.
func NewKeys(t *testing.T, chunks ...string) *Keys {
	t.Helper()
	k := &Keys{t: t, ch: make(chan []byte, 256), reads: make(chan struct{}, 16)}
	t.Cleanup(k.Close)
	k.Send(chunks...)
	return k
}

// Send queues chunks, one per Read.
func (k *Keys) Send(chunks ...string) {
	for _, c := range chunks {
		k.ch <- []byte(c)
	}
}

// Close makes Read return io.EOF after the queued chunks. It is idempotent.
func (k *Keys) Close() {
	k.closed.Do(func() { close(k.ch) })
}

// Waiters is the number of Reads blocked for a chunk.
func (k *Keys) Waiters() int {
	return int(k.waiters.Load())
}

// AwaitRead waits until a Read starts that has not been awaited yet.
func (k *Keys) AwaitRead() {
	k.t.Helper()
	Recv(k.t, k.reads)
}

// NextKey reads the next key through a fresh terminal, the way the next
// prompt on the same input would after a cancelled one.
func (k *Keys) NextKey() rune {
	k.t.Helper()
	ctx, cancel := context.WithTimeout(k.t.Context(), timeout)
	defer cancel()
	tio := &term.IO{In: k, Input: term.SharedInputState(k), Width: 20, Height: 2}
	permits := make(chan struct{}, 1)
	permits <- struct{}{}
	keys := tio.ReadEvents(ctx, permits)
	defer tio.AwaitReader()
	defer cancel()
	ev := <-keys
	assert.NoError(k.t, ev.Err)
	return ev.Key
}

func (k *Keys) Read(p []byte) (int, error) {
	select {
	case k.reads <- struct{}{}:
	default:
	}
	k.waiters.Add(1)
	chunk, ok := <-k.ch
	k.waiters.Add(-1)
	if !ok {
		return 0, io.EOF
	}
	return copy(p, chunk), nil
}
