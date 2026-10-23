// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"bytes"
	"context"
	"testing"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/internal/trie"

	"io"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestDropdownPressKeyCancelledReadKeepsNextInput(t *testing.T) {
	in := terst.NewKeys(t)
	d := newDropdown(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d.Ctx = ctx
	tio := &term.IO{In: in, Out: &bytes.Buffer{}}
	finished := make(chan error, 1)
	go func() {
		_, err := d.pressKey(d.Ctx, tio, &bytes.Buffer{}, 1, 1)
		finished <- err
	}()
	in.AwaitRead()
	cancel()
	err := terst.Recv(t, finished)
	assert.ErrorIs(t, err, context.Canceled)
	in.Send("x", terst.KeyEnter)
	assert.Equal(t, 'x', in.NextKey())
}

func TestDropdownPressKeyRuneAndRenderMore(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"alpha", "beta"}
	d.relevant = []int{0, 1}
	d.displayed = d.relevant
	d.trie = trie.NewTrie()
	d.trie.Add(ansi.Text("alpha"), 0)
	d.trie.Add(ansi.Text("beta"), 1)

	tio := terst.NewTermIO(20, 6)

	assert.Equal(t, 0, d.pressKeyRune(tio, term.KeyEnter, len(d.displayed), 4))

	d.selected = 1
	d.pressKeyRune(tio, '↑', len(d.displayed), 4)
	d.pressKeyRune(tio, '↓', len(d.displayed), 4)

	d.typed = []rune("ab")
	d.pressKeyRune(tio, 0x7f, len(d.displayed), 4)

	d.OneReturn = true
	d.typed = nil
	d.trie = trie.NewTrie()
	d.trie.Add(ansi.Text("abc"), 0)
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
	d.trie = trie.NewTrie()
	d.relevant = []int{0}
	d.displayed = d.relevant
	d.pressBackspace(terst.NewTermIO(10, 4))
	assert.Equal(t, "", string(d.typed))
	assert.True(t, !d.pressAny('z', len(d.displayed), 4))
}

func TestDropdownClearFrame(t *testing.T) {
	d := newDropdown(t.Context())
	io := terst.NewTermIO(10, 4)
	frame := &bytes.Buffer{}
	assert.NoError(t, d.clearFrame(io, frame, 2))
	buf, ok := io.Out.(*bytes.Buffer)
	assert.True(t, ok)
	assert.True(t, buf.Len() > 0)
}

func TestDropdownPressAnyOneReturn(t *testing.T) {
	d := newDropdown(t.Context())
	d.OneReturn = true
	d.trie = trie.NewTrie()
	d.trie.Add(ansi.Text("ok"), 0)

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
	io := terst.NewTermIO(10, 4)
	assert.Equal(t, 1, d.pressKeyRune(io, term.KeyEnter, len(d.displayed), 4))
	assert.Equal(t, 1, len(events))
	confirmed, ok := events[0].(dropdownConfirmed)
	assert.True(t, ok)
	assert.Equal(t, 1, confirmed.Selected)
}

func TestDropdownPressKeyRuneBackspace(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = trie.NewTrie()
	d.trie.Add(ansi.Text("a"), 0)
	d.relevant = []int{0}
	d.displayed = []int{0}
	d.Items = []any{"a"}
	d.typed = []rune("a")
	io := terst.NewTermIO(10, 4)
	d.pressKeyRune(io, 0x7f, len(d.displayed), 4)
	assert.Equal(t, 0, len(d.typed))
}

func TestDropdownPressKeyRuneArrows(t *testing.T) {
	d := newDropdown(t.Context())
	d.relevant = []int{0, 1}
	d.displayed = []int{0, 1}
	d.selected = 0
	io := terst.NewTermIO(10, 4)
	d.pressKeyRune(io, '↓', len(d.displayed), 4)
	assert.Equal(t, 1, d.selected)
	d.pressKeyRune(io, '↑', len(d.displayed), 4)
	assert.Equal(t, 0, d.selected)
}

func TestDropdownPressKeyRuneDefault(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = trie.NewTrie()
	d.trie.Add(ansi.Text("a"), 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	io := terst.NewTermIO(10, 4)
	assert.Equal(t, -1, d.pressKeyRune(io, 'a', len(d.displayed), 4))
	assert.Equal(t, "a", string(d.typed))
}

func TestDropdownPressKeyRuneEmitsStateEvents(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = trie.NewTrie()
	d.trie.Add(ansi.Text("alpha"), 0)
	d.trie.Add(ansi.Text("beta"), 1)
	d.Items = []any{"alpha", "beta"}
	d.relevant = []int{0, 1}
	d.displayed = []int{0, 1}
	d.selected = 1
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	io := terst.NewTermIO(10, 4)
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
	d.trie = trie.NewTrie()
	d.trie.Add(ansi.Text("a"), 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	io := terst.NewTermIO(10, 4)
	assert.Equal(t, 0, d.pressKeyRune(io, 'a', len(d.displayed), 4))
	assert.Equal(t, 2, len(events))
	_, ok := events[0].(dropdownFilterChanged)
	assert.True(t, ok)
	confirmed, ok := events[1].(dropdownConfirmed)
	assert.True(t, ok)
	assert.Equal(t, 0, confirmed.Selected)
}

func TestDropdownPressKeyReadsSharedPendingEnter(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "same terminal"
		if fresh {
			name = "fresh terminal"
		}
		t.Run(name, func(t *testing.T) {
			in := &terst.MockDescriptor{Reader: bytes.NewBufferString("\x1b[A\r"), FD: 0}
			first := &term.IO{In: in, Out: &bytes.Buffer{}}
			ev := <-first.ReadEvents(t.Context(), nil)
			first.AwaitReader()
			assert.NoError(t, ev.Err)
			assert.Equal(t, '↑', ev.Key)
			second := first
			if fresh {
				second = &term.IO{In: in, Out: &bytes.Buffer{}}
			}
			d := newDropdown(t.Context())
			d.Items = []any{"one", "two"}
			d.relevant = []int{0, 1}
			i, err := d.pressKey(d.Ctx, second, &bytes.Buffer{}, 1, 2)
			assert.NoError(t, err)
			assert.Equal(t, 0, i)
			ev = <-second.ReadEvents(t.Context(), nil)
			second.AwaitReader()
			assert.ErrorIs(t, ev.Err, io.EOF)
		})
	}
}
