// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"github.com/nfx/go-tui/internal/term"
	"io"
	"os"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestInputLeftArrowMovesCursorWithSplitEscape(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 60, 8)
	reader := &blockingByteReader{ch: make(chan []byte, 8)}
	t.Cleanup(reader.Close)
	input := &terst.MockDescriptor{Reader: reader, FD: 0}
	SetDefaultIO(input, cio)

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := Input(t.Context(), "PROMPT")
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
		SetDefaultIO(prevIn, prevOut)
	})
	in := bytes.NewBufferString("in")
	out := &terst.MockDescriptor{Writer: &bytes.Buffer{}, FD: 7}
	term.WriterChecker = func(int) bool { return true }
	term.GetSize = func(int) (int, int, error) { return 80, 24, nil }
	SetDefaultIO(in, out)
	_ = Stderr()

	_, defaultOut := term.DefaultStreams(t.Context())
	cio, ok := defaultOut.(*term.ChanIO)
	assert.True(t, ok)
	assert.NotNil(t, cio)

	d := newDropdown(t.Context())
	assert.Equal(t, in, d.in)
	assert.Equal(t, cio, d.out)

	s := newSpinners(t.Context())
	assert.Equal(t, in, s.In)
	assert.Equal(t, cio, s.Out)
	s.ticker.Stop()

	p := newProgressbar(t.Context())
	assert.Equal(t, in, p.In)
	assert.Equal(t, cio, p.Out)
	p.ticker.Stop()
}

func TestSetDefaultIO(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	in := bytes.NewBufferString("in")
	out := &bytes.Buffer{}
	SetDefaultIO(in, out)

	d := newDropdown(t.Context())
	assert.Equal(t, in, d.in)
	assert.Equal(t, out, d.out)

	s := newSpinners(t.Context())
	assert.Equal(t, in, s.In)
	assert.Equal(t, out, s.Out)
	s.ticker.Stop()

	p := newProgressbar(t.Context())
	assert.Equal(t, in, p.In)
	assert.Equal(t, out, p.Out)
	p.ticker.Stop()
}

func TestSpinnersCloseNil(t *testing.T) {
	var s *Spinners
	s.Close()
}

func TestInputDefaultTerminalPreservesCursorAcrossLogs(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 60, 8)
	reader := &blockingByteReader{ch: make(chan []byte, 32)}
	t.Cleanup(reader.Close)
	input := &terst.MockDescriptor{Reader: reader, FD: 0}
	SetDefaultIO(input, cio)

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := Input(t.Context(), "PROMPT", WithDefault("apple"))
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
	_, err := Stderr().Write([]byte("INF background\n"))
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

func TestNewIOWithTTY(t *testing.T) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		t.Skip("tty not available")
	}
	origStderr := os.Stderr
	os.Stderr = tty
	t.Cleanup(func() {
		os.Stderr = origStderr
		assert.NoError(t, tty.Close())
	})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio, err := NewIO(ctx)
	if err != nil {
		t.Fatalf("NewIO failed: %v", err)
	}
	if cio == nil {
		t.Fatalf("expected io")
	}
}

func TestInputDefaultTerminalHandlesTypedChars(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
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
	SetDefaultIO(input, cio)

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := Input(t.Context(), "Label")
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
	t.Cleanup(func() { SetDefaultIO(prevIn, prevOut) })
	in1, out1 := bytes.NewBufferString("1"), &bytes.Buffer{}
	in2, out2 := bytes.NewBufferString("2"), &bytes.Buffer{}
	SetDefaultIO(in1, out1)

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
			SetDefaultIO(in2, out2)
			SetDefaultIO(in1, out1)
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

func TestNewIOWithStubError(t *testing.T) {
	orig := term.GetSize
	term.GetSize = func(int) (int, int, error) { return 0, 0, errors.New("boom") }
	t.Cleanup(func() { term.GetSize = orig })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err := NewIO(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrNoTTY) {
		t.Fatalf("expected ErrNoTTY, got %v", err)
	}
}

func TestNewIOWithStubSize(t *testing.T) {
	orig := term.GetSize
	term.GetSize = func(int) (int, int, error) { return 80, 24, nil }
	t.Cleanup(func() { term.GetSize = orig })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio, err := NewIO(ctx)
	if err != nil {
		t.Fatalf("NewIO failed: %v", err)
	}
	if cio.Width != 80 || cio.Height != 24 {
		t.Fatalf("unexpected size %dx%d", cio.Width, cio.Height)
	}
}

func TestConfirmDefaultTerminalHandlesArrowKeys(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 40, 6)
	reader := &blockingByteReader{ch: make(chan []byte, 2)}
	t.Cleanup(reader.Close)
	input := &terst.MockDescriptor{
		Reader: reader,
		FD:     0,
	}
	SetDefaultIO(input, cio)

	result := make(chan bool, 1)
	go func() {
		result <- Confirm(t.Context(), "Are you sure?")
	}()

	initial := terst.WaitOutputContains(t, stdout.C, "Are you sure?", "Yes", "No")
	reader.SendBytes([]byte{0x1b, 0x5b, 0x42})
	updated := terst.WaitOutputContains(t, stdout.C, "Are you sure?", "Yes", "No")
	assert.True(t, initial != updated)
	reader.SendByte(term.KeyEnter)
	assert.True(t, !<-result)
}

func TestNewIOReturnsOrErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err := NewIO(ctx)
	if err != nil {
		return
	}
}
