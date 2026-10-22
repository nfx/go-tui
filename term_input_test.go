// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestSharedInputState_DistinctReadersSameDescriptor(t *testing.T) {
	first := &mockDescriptor{Reader: bytes.NewBufferString("a"), fd: 123}
	second := &mockDescriptor{Reader: bytes.NewBufferString("b"), fd: 123}
	tio := &termIO{in: first, input: sharedInputState(first)}
	read, accept := tio.startRead(t.Context())
	<-read.done
	if sharedInputState(second) == tio.input {
		t.Fatal("distinct readers shared state")
	}
	if sharedInputState(first) != tio.input {
		t.Fatal("reader identity did not retain state")
	}
	accept()
}

func TestSharedInputState_FileIsLocal(t *testing.T) {
	if sharedInputState(os.Stdin).source != nil {
		t.Fatal("plain files must not enter the shared registry")
	}
}

func TestDropdownPressKeyReadsSharedPendingEnter(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "same terminal"
		if fresh {
			name = "fresh terminal"
		}
		t.Run(name, func(t *testing.T) {
			in := &mockDescriptor{Reader: bytes.NewBufferString("\x1b[A\r"), fd: 0}
			first := &termIO{in: in, out: &bytes.Buffer{}}
			ev := <-first.readEvents(t.Context(), nil)
			first.awaitReader()
			assert.NoError(t, ev.err)
			assert.Equal(t, '↑', ev.key)
			second := first
			if fresh {
				second = &termIO{in: in, out: &bytes.Buffer{}}
			}
			d := newDropdown(t.Context())
			d.Items = []any{"one", "two"}
			d.relevant = []int{0, 1}
			i, err := d.pressKey(d.Ctx, second, &bytes.Buffer{}, 1, 2)
			assert.NoError(t, err)
			assert.Equal(t, 0, i)
			ev = <-second.readEvents(t.Context(), nil)
			second.awaitReader()
			assert.ErrorIs(t, ev.err, io.EOF)
		})
	}
}

func TestSharedInputState_UnacceptedCtrlCRetained(t *testing.T) {
	in := &mockDescriptor{Reader: bytes.NewBufferString("\x03"), fd: 0}
	first := &termIO{in: in}
	read, _ := first.startRead(t.Context())
	<-read.done
	if sharedInputState(in) != first.input {
		t.Fatal("unaccepted Ctrl-C was discarded as an empty failed read")
	}
	second := &termIO{in: in}
	ev := <-second.readEvents(t.Context(), nil)
	second.awaitReader()
	assert.ErrorIs(t, ev.err, io.EOF)
}

func TestSharedInputState_CancelledClosedReaderReleased(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	in := &notifyingReader{File: pr, started: make(chan struct{})}
	tio := &termIO{in: in}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tio.readEvents(ctx, nil)
	<-in.started
	cancel()
	tio.awaitReader()
	assert.NoError(t, pr.Close())
	<-tio.input.read.done
	assert.Error(t, tio.input.read.event.err)
	inputStates.Lock()
	_, retained := inputStates.states[in]
	inputStates.Unlock()
	if retained {
		t.Fatal("closed input retained after its cancelled read finished")
	}
}

func TestInput_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	value, err := Input(context.Background(), "Name",
		WithInput(strings.NewReader("alice\r")), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "alice", value)
	assert.Contains(t, out.String(), "Name")
}

func TestDropdown_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	// Down, Enter
	value, err := Dropdown(context.Background(), "Pick", []string{"a", "b", "c"},
		WithInput(strings.NewReader("\x1b[B\r")), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "b", value)
}

func TestConfirm_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	ok := Confirm(context.Background(), "Proceed",
		WithInput(strings.NewReader("\r")), WithOutput(&out))
	assert.True(t, ok)
}

func TestInput_ScriptedInputDefaultOutput(t *testing.T) {
	value, err := Input(context.Background(), "Name", WithInput(strings.NewReader("bob\r")), WithOutput(io.Discard))
	assert.NoError(t, err)
	assert.Equal(t, "bob", value)
}
