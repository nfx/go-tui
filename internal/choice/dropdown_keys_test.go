// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/tcommon"
	"github.com/nfx/go-tui/internal/term"

	"io"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/terst"
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

// pickAnswer is what a dropdown returned.
type pickAnswer struct {
	index int
	err   error
}

// startPick runs a dropdown of items on an ASCII test terminal, for keys typed
// while it waits.
func startPick(t *testing.T, items []any, o ...option.Opt) (*terst.Keys, *terst.Output, <-chan pickAnswer) {
	t.Helper()
	terst.UseASCII(t)
	keys, out := terst.UseTerminal(t)
	answer := make(chan pickAnswer, 1)
	go func() {
		i, err := DropdownIndex(t.Context(), "Pick", items, o...)
		answer <- pickAnswer{i, err}
	}()
	return keys, out, answer
}

// pickFooter is the hint row below the items of a dropdown.
const pickFooter = "| ^v move · type filter"

func TestDropdownArrowsMoveAndEnterConfirms(t *testing.T) {
	keys, out, answer := startPick(t, []any{"alpha", "beta", "gamma"})
	out.AwaitVisible("? Pick", "| > alpha", "|   beta", "|   gamma", pickFooter)
	keys.Type(terst.KeyDown)
	out.AwaitVisible("? Pick", "|   alpha", "| > beta", "|   gamma", pickFooter)
	keys.Type(terst.KeyUp)
	out.AwaitVisible("? Pick", "| > alpha", "|   beta", "|   gamma", pickFooter)
	keys.Type(terst.KeyDown + terst.KeyEnter)
	got := terst.Recv(t, answer)
	assert.NoError(t, got.err)
	assert.Equal(t, 1, got.index)
	// confirmation clears the frame and leaves the answer
	out.AwaitVisible("+ Pick · beta")
}

func TestDropdownTypingFiltersAndBackspaceRestores(t *testing.T) {
	keys, out, answer := startPick(t, []any{"alpha", "beta"})
	out.AwaitVisible("? Pick", "| > alpha", "|   beta", pickFooter)
	keys.Type("a")
	out.AwaitVisible("? Pick", "| > alpha", pickFooter)
	keys.Type(terst.KeyBackspace)
	out.AwaitVisible("? Pick", "| > alpha", "|   beta", pickFooter)
	// a key that matches nothing is not typed
	keys.Type("z" + terst.KeyEnter)
	got := terst.Recv(t, answer)
	assert.NoError(t, got.err)
	assert.Equal(t, 0, got.index)
}

func TestDropdownOneReturnConfirmsUniqueMatch(t *testing.T) {
	keys, out, answer := startPick(t, []any{"alpha", "beta"}, WithOneReturn())
	out.AwaitVisible("? Pick", "| > alpha", "|   beta", pickFooter)
	keys.Type("b")
	got := terst.Recv(t, answer)
	assert.NoError(t, got.err)
	assert.Equal(t, 1, got.index)
	out.AwaitVisible("+ Pick · beta")
}

func TestDropdownDownScrollsBelowTheFrame(t *testing.T) {
	items := make([]any, 30)
	for i := range items {
		items[i] = fmt.Sprintf("item-%02d", i)
	}
	// the 24-row terminal shows twelve items from first, the top one selected
	frame := func(first int) []string {
		rows := []string{"? Pick", fmt.Sprintf("| > item-%02d", first)}
		for i := first + 1; i < first+12; i++ {
			rows = append(rows, fmt.Sprintf("|   item-%02d", i))
		}
		return append(rows, fmt.Sprintf("| %d more · ^v move · type filter", 30-12-first))
	}
	keys, out, _ := startPick(t, items)
	out.AwaitVisible(frame(0)...)
	keys.Type(terst.KeyDown)
	out.AwaitVisible(frame(1)...)
}

func TestDropdownPressKeyReadsSharedPendingEnter(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "same terminal"
		if fresh {
			name = "fresh terminal"
		}
		t.Run(name, func(t *testing.T) {
			in := &tcommon.MockDescriptor{Reader: bytes.NewBufferString("\x1b[A\r"), FD: 0}
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
