// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestDropdownPressKeyCancelledReadKeepsNextInput(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	in := &notifyingReader{File: pr, started: make(chan struct{})}
	d := newDropdown(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d.Ctx = ctx
	tio := &termIO{in: in, out: &bytes.Buffer{}}
	finished := make(chan error, 1)
	go func() {
		_, err := d.pressKey(d.Ctx, tio, &bytes.Buffer{}, 1, 1)
		finished <- err
	}()
	select {
	case <-in.started:
	case <-time.After(time.Second):
		t.Fatal("dropdown did not start reading")
	}
	cancel()
	select {
	case err := <-finished:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled dropdown did not return")
	}
	_, err = pw.Write([]byte{'x', keyEnter})
	assert.NoError(t, err)
	next := newInput(t.Context(), "next")
	next.ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	next.in, next.out = in, &bytes.Buffer{}
	next.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	got, err := next.run(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
}

func TestDropdownPressKeyRuneAndRenderMore(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"alpha", "beta"}
	d.relevant = []int{0, 1}
	d.displayed = d.relevant
	d.trie = newTrie()
	d.trie.Add(text("alpha"), 0)
	d.trie.Add(text("beta"), 1)

	tio := newTestTermIO(20, 6)

	assert.Equal(t, 0, d.pressKeyRune(tio, keyEnter, len(d.displayed), 4))

	d.selected = 1
	d.pressKeyRune(tio, '↑', len(d.displayed), 4)
	d.pressKeyRune(tio, '↓', len(d.displayed), 4)

	d.typed = []rune("ab")
	d.pressKeyRune(tio, 0x7f, len(d.displayed), 4)

	d.OneReturn = true
	d.typed = nil
	d.trie = newTrie()
	d.trie.Add(text("abc"), 0)
	d.Items = []any{"abc"}
	d.relevant = []int{0}
	d.displayed = d.relevant
	assert.True(t, d.pressAny('a', len(d.displayed), 4))

	var frame bytes.Buffer
	assert.NoError(t, d.clearFrame(tio, &frame, 0))
}

func TestDropdownPressKeys(t *testing.T) {
	d := newDropdown(t.Context())
	d.relevant = []int{0, 1, 2, 3}
	d.displayed = []int{0, 1, 2}
	d.selected = 1

	d.pressDown(len(d.displayed))
	assert.Equal(t, 1, d.offset)
	d.pressUp(len(d.displayed))
	assert.Equal(t, 0, d.selected)

	d.typed = nil
	d.trie = newTrie()
	d.relevant = []int{0}
	d.displayed = d.relevant
	d.pressBackspace(newTestTermIO(10, 4))
	assert.Equal(t, "", string(d.typed))
	assert.True(t, !d.pressAny('z', len(d.displayed), 4))
}

func TestDropdownClearFrame(t *testing.T) {
	d := newDropdown(t.Context())
	io := newTestTermIO(10, 4)
	frame := &bytes.Buffer{}
	assert.NoError(t, d.clearFrame(io, frame, 2))
	buf, ok := io.out.(*bytes.Buffer)
	assert.True(t, ok)
	assert.True(t, buf.Len() > 0)
}

func TestDropdownPressAnyOneReturn(t *testing.T) {
	d := newDropdown(t.Context())
	d.OneReturn = true
	d.trie = newTrie()
	d.trie.Add(text("ok"), 0)

	assert.True(t, d.pressAny('o', 1, 1))
}

func TestDropdownPressKeyRuneEnter(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"a", "b"}
	d.relevant = []int{0, 1}
	d.displayed = []int{0, 1}
	d.selected = 1
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	io := newTestTermIO(10, 4)
	assert.Equal(t, 1, d.pressKeyRune(io, keyEnter, len(d.displayed), 4))
	assert.Equal(t, 1, len(events))
	confirmed, ok := events[0].(dropdownConfirmed)
	assert.True(t, ok)
	assert.Equal(t, 1, confirmed.Selected)
}

func TestDropdownPressKeyRuneBackspace(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = newTrie()
	d.trie.Add(text("a"), 0)
	d.relevant = []int{0}
	d.displayed = []int{0}
	d.Items = []any{"a"}
	d.typed = []rune("a")
	io := newTestTermIO(10, 4)
	d.pressKeyRune(io, 0x7f, len(d.displayed), 4)
	assert.Equal(t, 0, len(d.typed))
}

func TestDropdownPressKeyRuneArrows(t *testing.T) {
	d := newDropdown(t.Context())
	d.relevant = []int{0, 1}
	d.displayed = []int{0, 1}
	d.selected = 0
	io := newTestTermIO(10, 4)
	d.pressKeyRune(io, '↓', len(d.displayed), 4)
	assert.Equal(t, 1, d.selected)
	d.pressKeyRune(io, '↑', len(d.displayed), 4)
	assert.Equal(t, 0, d.selected)
}

func TestDropdownPressKeyRuneDefault(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = newTrie()
	d.trie.Add(text("a"), 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	io := newTestTermIO(10, 4)
	assert.Equal(t, -1, d.pressKeyRune(io, 'a', len(d.displayed), 4))
	assert.Equal(t, "a", string(d.typed))
}

func TestDropdownPressKeyRuneEmitsStateEvents(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = newTrie()
	d.trie.Add(text("alpha"), 0)
	d.trie.Add(text("beta"), 1)
	d.Items = []any{"alpha", "beta"}
	d.relevant = []int{0, 1}
	d.displayed = []int{0, 1}
	d.selected = 1
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	io := newTestTermIO(10, 4)
	assert.Equal(t, -1, d.pressKeyRune(io, 'a', len(d.displayed), 4))
	assert.Equal(t, 1, len(events))
	filtered, ok := events[0].(dropdownFilterChanged)
	assert.True(t, ok)
	assert.Equal(t, "a", filtered.Prefix)
	assert.Equal(t, 1, filtered.Matching)
	assert.Equal(t, 0, len(filtered.Added))
	assert.Equal(t, 1, len(filtered.Removed))
	assert.Equal(t, 1, filtered.Removed[0])
}

func TestDropdownPressKeyRuneOneReturn(t *testing.T) {
	d := newDropdown(t.Context())
	d.OneReturn = true
	d.trie = newTrie()
	d.trie.Add(text("a"), 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	io := newTestTermIO(10, 4)
	assert.Equal(t, 0, d.pressKeyRune(io, 'a', len(d.displayed), 4))
	assert.Equal(t, 2, len(events))
	_, ok := events[0].(dropdownFilterChanged)
	assert.True(t, ok)
	confirmed, ok := events[1].(dropdownConfirmed)
	assert.True(t, ok)
	assert.Equal(t, 0, confirmed.Selected)
}
