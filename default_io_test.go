// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestInputLeftArrowMovesCursorWithSplitEscape(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 60, 8)
	reader := &blockingByteReader{ch: make(chan []byte, 8)}
	t.Cleanup(reader.Close)
	input := &mockDescriptor{Reader: reader, fd: 0}
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

	waitOutputContains(t, stdout.C, "PROMPT")
	reader.SendByte('a')
	reader.SendByte('b')
	reader.SendByte('c')
	waitOutputContains(t, stdout.C, "PROMPT", "abc")
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	reader.SendByte('X')
	reader.SendByte(keyEnter)

	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "abXc", value)
	}
}

func TestWidgetsUseSharedDefaultTerminalOnTTY(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	prevTTY := terminalWriterChecker
	prevSize := termGetSize
	t.Cleanup(func() {
		terminalWriterChecker = prevTTY
		termGetSize = prevSize
		SetDefaultIO(prevIn, prevOut)
	})
	in := bytes.NewBufferString("in")
	out := &mockDescriptor{Writer: &bytes.Buffer{}, fd: 7}
	terminalWriterChecker = func(int) bool { return true }
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	SetDefaultIO(in, out)
	_ = Stderr()

	_, defaultOut := defaultStreams(t.Context())
	cio, ok := defaultOut.(*chanIO)
	assert.True(t, ok)
	assert.NotNil(t, cio)

	d := newDropdown(t.Context())
	assert.Equal(t, in, d.in)
	assert.Equal(t, cio, d.out)

	input := newInput(t.Context(), "label")
	assert.Equal(t, in, input.In)
	assert.Equal(t, cio, input.Out)

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
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	in := bytes.NewBufferString("in")
	out := &bytes.Buffer{}
	SetDefaultIO(in, out)

	d := newDropdown(t.Context())
	assert.Equal(t, in, d.in)
	assert.Equal(t, out, d.out)

	input := newInput(t.Context(), "label")
	assert.Equal(t, in, input.In)
	assert.Equal(t, out, input.Out)

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
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 60, 8)
	reader := &blockingByteReader{ch: make(chan []byte, 32)}
	t.Cleanup(reader.Close)
	input := &mockDescriptor{Reader: reader, fd: 0}
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

	waitOutputContains(t, stdout.C, "PROMPT", "apple")
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	_, err := Stderr().Write([]byte("INF background\n"))
	assert.NoError(t, err)
	waitOutputContains(t, stdout.C, "INF background", "PROMPT", "apple")
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	reader.SendByte('q')
	reader.SendByte('q')
	reader.SendByte('q')
	reader.SendByte(keyEnter)

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
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 40, 6)
	input := &mockDescriptor{
		Reader: &chunkReader{chunks: [][]byte{
			{'h'},
			{'i'},
			{keyEnter},
		}},
		fd: 0,
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
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
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
		in, out := defaultStreams(t.Context())
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
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 0, 0, errors.New("boom") }
	t.Cleanup(func() { termGetSize = orig })
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
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	t.Cleanup(func() { termGetSize = orig })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio, err := NewIO(ctx)
	if err != nil {
		t.Fatalf("NewIO failed: %v", err)
	}
	if cio.width != 80 || cio.height != 24 {
		t.Fatalf("unexpected size %dx%d", cio.width, cio.height)
	}
}

func TestConfirmDefaultTerminalHandlesArrowKeys(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 40, 6)
	reader := &blockingByteReader{ch: make(chan []byte, 2)}
	t.Cleanup(reader.Close)
	input := &mockDescriptor{
		Reader: reader,
		fd:     0,
	}
	SetDefaultIO(input, cio)

	result := make(chan bool, 1)
	go func() {
		result <- Confirm(t.Context(), "Are you sure?")
	}()

	initial := waitOutputContains(t, stdout.C, "Are you sure?", "Yes", "No")
	reader.SendBytes([]byte{0x1b, 0x5b, 0x42})
	updated := waitOutputContains(t, stdout.C, "Are you sure?", "Yes", "No")
	assert.True(t, initial != updated)
	reader.SendByte(keyEnter)
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
