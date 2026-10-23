// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"io"
	"testing"
	"time"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/term"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestInputLeftArrowMovesCursorWithSplitEscape(t *testing.T) {
	reader, stdout := terst.UseTerminal(t)

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := tui.Input(t.Context(), "PROMPT")
		if err != nil {
			errs <- err
			return
		}
		result <- value
	}()

	stdout.AwaitOutput("PROMPT")
	reader.Send("a", "b", "c")
	stdout.AwaitOutput("PROMPT", "abc")
	reader.Send(terst.KeyLeft, "X", terst.KeyEnter)

	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "abXc", value)
	}
}

func TestWidgetsUseSharedDefaultTerminalOnTTY(t *testing.T) {
	in := bytes.NewBufferString("in")
	out := &terst.MockDescriptor{Writer: &bytes.Buffer{}, FD: 7}
	terst.Swap(t, &term.WriterChecker, func(int) bool { return true })
	terst.Swap(t, &term.GetSize, func(int) (int, int, error) { return 80, 24, nil })
	terst.UseIO(t, in, out)
	_ = tui.Stderr()

	_, defaultOut := term.DefaultStreams(t.Context())
	cio, ok := defaultOut.(*term.ChanIO)
	assert.True(t, ok)
	assert.NotNil(t, cio)
}

func TestSpinnersCloseNil(t *testing.T) {
	var s *tui.Spinners
	s.Close()
}

func TestInputDefaultTerminalPreservesCursorAcrossLogs(t *testing.T) {
	reader, stdout := terst.UseTerminal(t)

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := tui.Input(t.Context(), "PROMPT", tui.WithDefault("apple"))
		if err != nil {
			errs <- err
			return
		}
		result <- value
	}()

	stdout.AwaitOutput("PROMPT", "apple")
	reader.Send(terst.KeyLeft, terst.KeyLeft)
	_, err := tui.Stderr().Write([]byte("INF background\n"))
	assert.NoError(t, err)
	stdout.AwaitOutput("INF background", "PROMPT", "apple")
	reader.Send(terst.KeyLeft, "q", "q", "q", terst.KeyEnter)

	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "apqqqple", value)
	}
}

func TestInputDefaultTerminalHandlesTypedChars(t *testing.T) {
	reader := terst.UseKeys(t)
	reader.Send("h", "i", terst.KeyEnter)

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := tui.Input(t.Context(), "Label")
		if err != nil {
			errs <- err
			return
		}
		result <- value
	}()

	// the keys are queued before the prompt reads, so intermediate frames may
	// be coalesced; only the final result is deterministic.
	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "hi", value)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Input to return")
	}
}

func TestDefaultStreamsArePairedUnderSetDefaultIO(t *testing.T) {
	in1, out1 := bytes.NewBufferString("1"), &bytes.Buffer{}
	in2, out2 := bytes.NewBufferString("2"), &bytes.Buffer{}
	terst.UseIO(t, in1, out1)

	stop := make(chan struct{})
	swapped := make(chan struct{})
	go func() {
		defer close(swapped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			term.Default.SetIO(in2, out2)
			term.Default.SetIO(in1, out1)
		}
	}()
	for range 1000 {
		in, out := term.DefaultStreams(t.Context())
		paired := (in == io.Reader(in1) && out == io.Writer(out1)) ||
			(in == io.Reader(in2) && out == io.Writer(out2))
		if !paired {
			t.Fatalf("mismatched default streams: %p %p", in, out)
		}
	}
	close(stop)
	<-swapped
}

func TestConfirmDefaultTerminalHandlesArrowKeys(t *testing.T) {
	reader, stdout := terst.UseTerminal(t)

	result := make(chan bool, 1)
	go func() {
		result <- tui.Confirm(t.Context(), "Are you sure?")
	}()

	initial := stdout.AwaitOutput("Are you sure?", "Yes", "No")
	reader.Send(terst.KeyDown)
	updated := stdout.AwaitOutput("Are you sure?", "Yes", "No")
	assert.True(t, initial != updated)
	reader.Send(terst.KeyEnter)
	assert.True(t, !<-result)
}
