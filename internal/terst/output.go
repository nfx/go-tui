// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// Output records what a test terminal receives, one chunk per write, and
// applies it to an emulated screen. Writes never block. Waits for chunks
// consume them in order, each at most once; waits for the screen don't. All
// waits fail the test after the timeout.
type Output struct {
	t       *testing.T
	mu      sync.Mutex
	chunks  []string
	next    int
	screen  screen
	changed chan struct{} // closed and replaced on every write
}

// NewOutput returns an empty recording for the waits of t, on an 80×24 screen.
func NewOutput(t *testing.T) *Output {
	t.Helper()
	return &Output{t: t, screen: screen{width: 80, height: 24}, changed: make(chan struct{})}
}

// Resize sets the size of the screen for later writes. With a zero height,
// rows below the bottom are added instead of scrolling.
func (o *Output) Resize(width, height int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.screen.width, o.screen.height = width, height
}

func (o *Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.chunks = append(o.chunks, string(p))
	o.screen.write(p)
	close(o.changed)
	o.changed = make(chan struct{})
	return len(p), nil
}

// String is everything written so far, read or not.
func (o *Output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.chunks, "")
}

// Visible is what the screen shows: its rows that are not blank, without
// surrounding spaces, one per line.
func (o *Output) Visible() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.screen.rows(), "\n")
}

// AwaitVisible waits until the screen shows exactly the given rows, as
// [Output.Visible] has them. A wanted row ending with "..." only has to start
// with what precedes it.
func (o *Output) AwaitVisible(want ...string) {
	o.t.Helper()
	deadline := time.After(timeout)
	for {
		o.mu.Lock()
		shown, changed := o.screen.shows(want), o.changed
		o.mu.Unlock()
		if shown {
			return
		}
		select {
		case <-changed:
		case <-deadline:
			o.t.Fatalf("timed out waiting for rows %q, screen shows %q", want, strings.Split(o.Visible(), "\n"))
		}
	}
}

// Next returns the next unread chunk.
func (o *Output) Next() string {
	o.t.Helper()
	got, ok := o.await(func(string) bool { return true })
	if !ok {
		o.t.Fatalf("timed out waiting for output, got %q", o.String())
	}
	return got
}

// AwaitOutput returns the next chunk that contains all parts, skipping the
// chunks before it.
func (o *Output) AwaitOutput(parts ...string) string {
	o.t.Helper()
	got, ok := o.await(func(chunk string) bool {
		for _, part := range parts {
			if !strings.Contains(chunk, part) {
				return false
			}
		}
		return true
	})
	if !ok {
		o.t.Fatalf("timed out waiting for output containing %q, got %q", parts, o.String())
	}
	return got
}

// Unread consumes and returns every unread chunk, without waiting.
func (o *Output) Unread() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	rest := strings.Join(o.chunks[o.next:], "")
	o.next = len(o.chunks)
	return rest
}

// await consumes chunks until one matches, or reports false after the timeout.
func (o *Output) await(match func(string) bool) (string, bool) {
	deadline := time.After(timeout)
	for {
		o.mu.Lock()
		changed := o.changed
		for o.next < len(o.chunks) {
			chunk := o.chunks[o.next]
			o.next++
			if match(chunk) {
				o.mu.Unlock()
				return chunk, true
			}
		}
		o.mu.Unlock()
		select {
		case <-changed:
		case <-deadline:
			return "", false
		}
	}
}
