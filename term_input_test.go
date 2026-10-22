// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"io"
	"os"
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
