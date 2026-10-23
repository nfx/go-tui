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
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		term.Default.SetIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 60, 8)
	reader := terst.NewBlockingByteReader(8)
	t.Cleanup(reader.Close)
	input := &terst.MockDescriptor{Reader: reader, FD: 0}
	term.Default.SetIO(input, cio)

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

	terst.WaitOutputContains(t, stdout.C, "PROMPT")
	reader.SendByte('a')
	reader.SendByte('b')
	reader.SendByte('c')
	terst.WaitOutputContains(t, stdout.C, "PROMPT", "abc")
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	reader.SendByte('X')
	reader.SendByte(term.KeyEnter)

	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "abXc", value)
	}
}

func TestWidgetsUseSharedDefaultTerminalOnTTY(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	prevTTY := term.WriterChecker
	prevSize := term.GetSize
	t.Cleanup(func() {
		term.WriterChecker = prevTTY
		term.GetSize = prevSize
		term.Default.SetIO(prevIn, prevOut)
	})
	in := bytes.NewBufferString("in")
	out := &terst.MockDescriptor{Writer: &bytes.Buffer{}, FD: 7}
	term.WriterChecker = func(int) bool { return true }
	term.GetSize = func(int) (int, int, error) { return 80, 24, nil }
	term.Default.SetIO(in, out)
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
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		term.Default.SetIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 60, 8)
	reader := terst.NewBlockingByteReader(32)
	t.Cleanup(reader.Close)
	input := &terst.MockDescriptor{Reader: reader, FD: 0}
	term.Default.SetIO(input, cio)

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

	terst.WaitOutputContains(t, stdout.C, "PROMPT", "apple")
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	_, err := tui.Stderr().Write([]byte("INF background\n"))
	assert.NoError(t, err)
	terst.WaitOutputContains(t, stdout.C, "INF background", "PROMPT", "apple")
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	reader.SendByte('q')
	reader.SendByte('q')
	reader.SendByte('q')
	reader.SendByte(term.KeyEnter)

	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "apqqqple", value)
	}
}

func TestInputDefaultTerminalHandlesTypedChars(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		term.Default.SetIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 40, 6)
	input := &terst.MockDescriptor{
		Reader: &terst.ChunkReader{Chunks: [][]byte{
			{'h'},
			{'i'},
			{term.KeyEnter},
		}},
		FD: 0,
	}
	term.Default.SetIO(input, cio)

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

	// chunkReader delivers all keys at once, so intermediate frames may be
	// coalesced; only the final result is deterministic.
	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "hi", value)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Input to return")
	}
	_ = stdout
}

func TestDefaultStreamsArePairedUnderSetDefaultIO(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() { term.Default.SetIO(prevIn, prevOut) })
	in1, out1 := bytes.NewBufferString("1"), &bytes.Buffer{}
	in2, out2 := bytes.NewBufferString("2"), &bytes.Buffer{}
	term.Default.SetIO(in1, out1)

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
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		term.Default.SetIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 40, 6)
	reader := terst.NewBlockingByteReader(2)
	t.Cleanup(reader.Close)
	input := &terst.MockDescriptor{
		Reader: reader,
		FD:     0,
	}
	term.Default.SetIO(input, cio)

	result := make(chan bool, 1)
	go func() {
		result <- tui.Confirm(t.Context(), "Are you sure?")
	}()

	initial := terst.WaitOutputContains(t, stdout.C, "Are you sure?", "Yes", "No")
	reader.SendBytes([]byte{0x1b, 0x5b, 0x42})
	updated := terst.WaitOutputContains(t, stdout.C, "Are you sure?", "Yes", "No")
	assert.True(t, initial != updated)
	reader.SendByte(term.KeyEnter)
	assert.True(t, !<-result)
}
