// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

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

// Keys is scripted keyboard input: Read returns at most one chunk, blocking
// until one is sent.
type Keys struct {
	t       *testing.T
	ch      chan []byte
	reads   chan struct{}
	waiters atomic.Int32
	closed  sync.Once
	mu      sync.Mutex
	rest    []byte // the unread tail of the last chunk
}

// NewKeys returns input that serves chunks, one per Read, and then blocks until
// more are sent. A Read shorter than a chunk leaves its tail for the next Read,
// as a pipe does. It stays open until [Keys.Close] or the end of the test.
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

// Type queues script one key per chunk, as a terminal delivers keystrokes:
// a chunk of several keys reads as a paste. Escape sequences, Alt+key and a
// bracketed paste stay whole.
func (k *Keys) Type(script string) {
	for script != "" {
		n := k.keyLen(script)
		k.Send(script[:n])
		script = script[n:]
	}
}

// keyLen is the length of the key at the start of script.
func (*Keys) keyLen(script string) int {
	const pasteStart, pasteEnd = "\x1b[200~", "\x1b[201~"
	if strings.HasPrefix(script, pasteStart) {
		if end := strings.Index(script, pasteEnd); end >= 0 {
			return end + len(pasteEnd)
		}
		return len(script)
	}
	if script[0] != KeyEsc[0] || len(script) == 1 {
		_, size := utf8.DecodeRuneInString(script)
		return size
	}
	switch b := script[1]; {
	case b == '[':
		for i := 2; i < len(script); i++ {
			if script[i] >= 0x40 && script[i] <= 0x7e {
				return i + 1
			}
		}
		return len(script)
	case b == 'O' && len(script) > 2:
		return 3
	case b >= 0x20 && b <= 0x7e:
		return 2 // Alt+key
	default:
		return 1
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

// AwaitRead waits until a Read starts waiting for a chunk, one that has not
// been awaited yet.
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
	k.mu.Lock()
	if len(k.rest) > 0 {
		n := copy(p, k.rest)
		k.rest = k.rest[n:]
		k.mu.Unlock()
		return n, nil
	}
	k.mu.Unlock()
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
	n := copy(p, chunk)
	k.mu.Lock()
	k.rest = chunk[n:]
	k.mu.Unlock()
	return n, nil
}
