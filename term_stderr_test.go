// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestStderrReturnsRawWriterWithoutTTY(t *testing.T) {
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
	out := &bytes.Buffer{}
	terminalWriterChecker = func(int) bool { return false }
	SetDefaultIO(in, out)

	assert.Equal(t, out, Stderr())
	_, defaultOut := defaultStreams(t.Context())
	assert.Equal(t, out, defaultOut)
}

func TestStderrReturnsCoordinatedWriterOnTTY(t *testing.T) {
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
	out := &mockDescriptor{Writer: &bytes.Buffer{}, fd: 42}
	terminalWriterChecker = func(int) bool { return true }
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	SetDefaultIO(in, out)

	stderr := Stderr()
	fdw, ok := stderr.(interface{ Fd() uintptr })
	assert.True(t, ok)
	assert.Equal(t, uintptr(42), fdw.Fd())
	_, defaultOut := defaultStreams(t.Context())
	cio, ok := defaultOut.(*chanIO)
	assert.True(t, ok)
	assert.NotNil(t, cio)
	ew, ok := stderr.(*terminalStderr)
	assert.True(t, ok)
	assert.Equal(t, defaultIO, ew.parent)
	assert.Equal(t, cio, defaultIO.arbiter)
}

func TestStderrReturnsRawWriterOnTTYInCI(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	prevSize := termGetSize
	t.Cleanup(func() {
		termGetSize = prevSize
		SetDefaultIO(prevIn, prevOut)
	})
	withEnv(t, "CI=true")
	out := ttyWriter(t)
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	SetDefaultIO(bytes.NewBufferString("in"), out)

	assert.Equal(t, out, Stderr())
	_, defaultOut := defaultStreams(t.Context())
	assert.Equal(t, out, defaultOut)
	assert.True(t, defaultIO.arbiter == nil)
}

func TestConfirmAndStderrShareDefaultTerminal(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 40, 6)
	inputR, inputW, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, inputR.Close())
		assert.NoError(t, inputW.Close())
	})
	SetDefaultIO(&fdByteReader{f: inputR}, cio)

	result := make(chan bool, 1)
	go func() {
		result <- Confirm(t.Context(), "Are you sure?")
	}()

	waitOutputContains(t, stdout.C, "Are you sure?")
	_, err = Stderr().Write([]byte("INF foo bar=baz\n"))
	assert.NoError(t, err)
	waitOutputContains(t, stdout.C, "INF foo bar=baz", "Are you sure?")
	_, err = inputW.Write([]byte{keyEnter})
	assert.NoError(t, err)
	assert.True(t, <-result)
	answer := waitOutputContains(t, stdout.C, "✔", "Are you sure?")
	assert.Equal(t, 0, strings.Count(answer, "→"))
}

func TestInputAndStderrShareDefaultTerminal(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 60, 8)
	reader := &blockingByteReader{ch: make(chan []byte, 3)}
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
	_, err := Stderr().Write([]byte("INF background\n"))
	assert.NoError(t, err)
	logged := waitOutputContains(t, stdout.C, "PROMPT", "INF background")
	assert.Equal(t, 1, strings.Count(logged, "PROMPT"))
	reader.SendByte('a')
	reader.SendByte('b')
	updated := waitOutputContains(t, stdout.C, "PROMPT", "ab")
	assert.Equal(t, 1, strings.Count(updated, "PROMPT"))
	reader.SendByte(keyEnter)
	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "ab", value)
	}
}

func TestStderrWritesUseNativeHistoryWhilePromptActive(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 60, 8)
	inputR, inputW, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, inputR.Close())
		assert.NoError(t, inputW.Close())
	})
	SetDefaultIO(&fdByteReader{f: inputR}, cio)

	done := make(chan bool, 1)
	go func() {
		done <- Confirm(t.Context(), "scrollback")
	}()

	waitOutputContains(t, stdout.C, "scrollback")
	_, err = Stderr().Write([]byte("INF first\n"))
	assert.NoError(t, err)
	first := waitOutputContains(t, stdout.C, "INF first", "scrollback")
	assert.Equal(t, 1, strings.Count(first, "INF first"))

	_, err = Stderr().Write([]byte("INF second\n"))
	assert.NoError(t, err)
	second := waitOutputContains(t, stdout.C, "INF second", "scrollback")
	assert.Equal(t, 0, strings.Count(second, "INF first"))
	assert.Equal(t, 1, strings.Count(second, "INF second"))

	_, err = inputW.Write([]byte{keyEnter})
	assert.NoError(t, err)
	assert.True(t, <-done)
}

func TestStderrNormalizesLoneLFWithoutOverlay(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 60, 8)
	SetDefaultIO(prevIn, cio)

	_, err := Stderr().Write([]byte("one\ntwo\n"))
	assert.NoError(t, err)
	assert.Equal(t, "one\r\ntwo\r\n", <-stdout.C)
}

func TestStderrPreservesCRLFAcrossChunkBoundary(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 60, 8)
	SetDefaultIO(prevIn, cio)

	_, err := Stderr().Write([]byte("one\r"))
	assert.NoError(t, err)
	assert.Equal(t, "one\r", <-stdout.C)

	_, err = Stderr().Write([]byte("\ntwo\n"))
	assert.NoError(t, err)
	assert.Equal(t, "\ntwo\r\n", <-stdout.C)
}

func TestTerminalStderrImplementsCloser(t *testing.T) {
	prevTTY := terminalWriterChecker
	prevSize := termGetSize
	t.Cleanup(func() {
		terminalWriterChecker = prevTTY
		termGetSize = prevSize
	})

	var out bytes.Buffer
	outW := &mockDescriptor{Writer: &out, fd: 7}
	terminalWriterChecker = func(int) bool { return true }
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }

	tioInst := &tio{reader: bytes.NewBufferString(""), writer: outW}
	stderr := tioInst.stderrWriter()

	closer, ok := stderr.(io.Closer)
	assert.True(t, ok)

	_, err := stderr.Write([]byte("flushed\n"))
	assert.NoError(t, err)

	assert.NoError(t, closer.Close())
	assert.True(t, strings.Contains(out.String(), "flushed"))
}

func TestTerminalStderrSyncFlushesWithoutClosing(t *testing.T) {
	prevTTY := terminalWriterChecker
	prevSize := termGetSize
	t.Cleanup(func() {
		terminalWriterChecker = prevTTY
		termGetSize = prevSize
	})

	var out bytes.Buffer
	outW := &mockDescriptor{Writer: &out, fd: 7}
	terminalWriterChecker = func(int) bool { return true }
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }

	tioInst := &tio{reader: bytes.NewBufferString(""), writer: outW}
	stderr := tioInst.stderrWriter()

	syncer, ok := stderr.(interface{ Sync() error })
	assert.True(t, ok)

	_, err := stderr.Write([]byte("first\n"))
	assert.NoError(t, err)
	assert.NoError(t, syncer.Sync())
	assert.True(t, strings.Contains(out.String(), "first"))

	// arbiter must still be alive: a second write should succeed.
	_, err = stderr.Write([]byte("second\n"))
	assert.NoError(t, err)
	assert.NoError(t, syncer.Sync())
	assert.True(t, strings.Contains(out.String(), "second"))

	assert.NoError(t, tioInst.Close())
}
