// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"io"
	"sync"
	"testing"
	"time"
)

// failsafe turns a hang into a failure; it never orders events.
const failsafe = 5 * time.Second

// keyboardFd is never a terminal, so prompts on the keyboard skip raw mode.
const keyboardFd = 1 << 20

const (
	keyEnter     = "\r"
	keyDown      = "\x1b[B"
	keyBackspace = "\x7f"
)

// keyboard is the input of the terminal. Every Read blocks until the test
// completes it, so a prompt waiting for a key is an observable state.
type keyboard struct {
	reads  chan chan<- string
	closed chan struct{}
	mu     sync.Mutex
	rest   []byte // the part of a key that did not fit into the last read
}

func newKeyboard(t *testing.T) *keyboard {
	t.Helper()
	k := &keyboard{
		reads:  make(chan chan<- string),
		closed: make(chan struct{}),
	}
	t.Cleanup(func() { close(k.closed) })
	return k
}

func (k *keyboard) Fd() uintptr {
	return keyboardFd
}

// Read returns one key per call, as a terminal in raw mode does. A key
// longer than p, such as a terminal reply, is returned over several reads.
func (k *keyboard) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	k.mu.Lock()
	if len(k.rest) > 0 {
		n := copy(p, k.rest)
		k.rest = k.rest[n:]
		k.mu.Unlock()
		return n, nil
	}
	k.mu.Unlock()
	reply := make(chan string, 1)
	select {
	case <-k.closed:
		return 0, io.EOF
	case k.reads <- reply:
	}
	select {
	case <-k.closed:
		return 0, io.EOF
	case key := <-reply:
		n := copy(p, key)
		k.mu.Lock()
		k.rest = []byte(key[n:])
		k.mu.Unlock()
		return n, nil
	}
}

// blockedRead waits until a prompt is blocked reading a key and returns that
// read, so that the test decides when and with which key it completes.
func (k *keyboard) blockedRead(t *testing.T) chan<- string {
	t.Helper()
	select {
	case read := <-k.reads:
		return read
	case <-time.After(failsafe):
		t.Fatal("no prompt is reading a key")
		return nil
	}
}

// press completes the next blocked read with key.
func (k *keyboard) press(t *testing.T, key string) {
	t.Helper()
	k.blockedRead(t) <- key
}
