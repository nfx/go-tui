// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

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
	tio := &IO{In: first, Input: SharedInputState(first)}
	read, accept := tio.StartRead(t.Context())
	<-read.Done
	if SharedInputState(second) == tio.Input {
		t.Fatal("distinct readers shared state")
	}
	if SharedInputState(first) != tio.Input {
		t.Fatal("reader identity did not retain state")
	}
	accept()
}

func TestSharedInputState_FileIsLocal(t *testing.T) {
	if SharedInputState(os.Stdin).source != nil {
		t.Fatal("plain files must not enter the shared registry")
	}
}

func TestSharedInputState_UnacceptedCtrlCRetained(t *testing.T) {
	in := &mockDescriptor{Reader: bytes.NewBufferString("\x03"), fd: 0}
	first := &IO{In: in}
	read, _ := first.StartRead(t.Context())
	<-read.Done
	if SharedInputState(in) != first.Input {
		t.Fatal("unaccepted Ctrl-C was discarded as an empty failed read")
	}
	second := &IO{In: in}
	ev := <-second.ReadEvents(t.Context(), nil)
	second.AwaitReader()
	assert.ErrorIs(t, ev.Err, io.EOF)
}
