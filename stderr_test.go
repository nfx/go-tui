// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"github.com/nfx/go-tui/internal/term"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestStderrReturnsRawWriterWithoutTTY(t *testing.T) {
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
	out := &bytes.Buffer{}
	term.WriterChecker = func(int) bool { return false }
	SetDefaultIO(in, out)

	assert.Equal(t, out, Stderr())
	_, defaultOut := term.DefaultStreams(t.Context())
	assert.Equal(t, out, defaultOut)
}

func TestStderrReturnsCoordinatedWriterOnTTY(t *testing.T) {
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
	out := &terst.MockDescriptor{Writer: &bytes.Buffer{}, FD: 42}
	term.WriterChecker = func(int) bool { return true }
	term.GetSize = func(int) (int, int, error) { return 80, 24, nil }
	SetDefaultIO(in, out)

	stderr := Stderr()
	fdw, ok := stderr.(interface{ Fd() uintptr })
	assert.True(t, ok)
	assert.Equal(t, uintptr(42), fdw.Fd())
	_, defaultOut := term.DefaultStreams(t.Context())
	cio, ok := defaultOut.(*term.ChanIO)
	assert.True(t, ok)
	assert.NotNil(t, cio)
	ew, ok := stderr.(*term.TerminalStderr)
	assert.True(t, ok)
	assert.Equal(t, term.Default, ew.Parent)
	assert.Equal(t, cio, term.Default.Arbiter)
}

func TestStderrReturnsRawWriterOnTTYInCI(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	prevSize := term.GetSize
	t.Cleanup(func() {
		term.GetSize = prevSize
		SetDefaultIO(prevIn, prevOut)
	})
	terst.WithEnv(t, "CI=true")
	out := terst.TTYWriter(t)
	term.GetSize = func(int) (int, int, error) { return 80, 24, nil }
	SetDefaultIO(bytes.NewBufferString("in"), out)

	assert.Equal(t, out, Stderr())
	_, defaultOut := term.DefaultStreams(t.Context())
	assert.Equal(t, out, defaultOut)
	assert.True(t, term.Default.Arbiter == nil)
}

func TestConfirmAndStderrShareDefaultTerminal(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 40, 6)
	inputR, inputW, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, inputR.Close())
		assert.NoError(t, inputW.Close())
	})
	SetDefaultIO(&terst.FDByteReader{F: inputR}, cio)

	result := make(chan bool, 1)
	go func() {
		result <- Confirm(t.Context(), "Are you sure?")
	}()

	terst.WaitOutputContains(t, stdout.C, "Are you sure?")
	_, err = Stderr().Write([]byte("INF foo bar=baz\n"))
	assert.NoError(t, err)
	terst.WaitOutputContains(t, stdout.C, "INF foo bar=baz", "Are you sure?")
	_, err = inputW.Write([]byte{term.KeyEnter})
	assert.NoError(t, err)
	assert.True(t, <-result)
	answer := terst.WaitOutputContains(t, stdout.C, "✔", "Are you sure?")
	assert.Equal(t, 0, strings.Count(answer, "→"))
}

func TestInputAndStderrShareDefaultTerminal(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 60, 8)
	reader := terst.NewBlockingByteReader(3)
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
	_, err := Stderr().Write([]byte("INF background\n"))
	assert.NoError(t, err)
	logged := terst.WaitOutputContains(t, stdout.C, "PROMPT", "INF background")
	assert.Equal(t, 1, strings.Count(logged, "PROMPT"))
	reader.SendByte('a')
	reader.SendByte('b')
	updated := terst.WaitOutputContains(t, stdout.C, "PROMPT", "ab")
	assert.Equal(t, 1, strings.Count(updated, "PROMPT"))
	reader.SendByte(term.KeyEnter)
	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "ab", value)
	}
}

func TestStderrWritesUseNativeHistoryWhilePromptActive(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 60, 8)
	inputR, inputW, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, inputR.Close())
		assert.NoError(t, inputW.Close())
	})
	SetDefaultIO(&terst.FDByteReader{F: inputR}, cio)

	done := make(chan bool, 1)
	go func() {
		done <- Confirm(t.Context(), "scrollback")
	}()

	terst.WaitOutputContains(t, stdout.C, "scrollback")
	_, err = Stderr().Write([]byte("INF first\n"))
	assert.NoError(t, err)
	first := terst.WaitOutputContains(t, stdout.C, "INF first", "scrollback")
	assert.Equal(t, 1, strings.Count(first, "INF first"))

	_, err = Stderr().Write([]byte("INF second\n"))
	assert.NoError(t, err)
	second := terst.WaitOutputContains(t, stdout.C, "INF second", "scrollback")
	assert.Equal(t, 0, strings.Count(second, "INF first"))
	assert.Equal(t, 1, strings.Count(second, "INF second"))

	_, err = inputW.Write([]byte{term.KeyEnter})
	assert.NoError(t, err)
	assert.True(t, <-done)
}

func TestStderrNormalizesLoneLFWithoutOverlay(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 60, 8)
	SetDefaultIO(prevIn, cio)

	_, err := Stderr().Write([]byte("one\ntwo\n"))
	assert.NoError(t, err)
	assert.Equal(t, "one\r\ntwo\r\n", <-stdout.C)
}

func TestStderrPreservesCRLFAcrossChunkBoundary(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := terst.ChainIOForTest(t, 60, 8)
	SetDefaultIO(prevIn, cio)

	_, err := Stderr().Write([]byte("one\r"))
	assert.NoError(t, err)
	assert.Equal(t, "one\r", <-stdout.C)

	_, err = Stderr().Write([]byte("\ntwo\n"))
	assert.NoError(t, err)
	assert.Equal(t, "\ntwo\r\n", <-stdout.C)
}

func TestTerminalStderrImplementsCloser(t *testing.T) {
	prevTTY := term.WriterChecker
	prevSize := term.GetSize
	t.Cleanup(func() {
		term.WriterChecker = prevTTY
		term.GetSize = prevSize
	})

	var out bytes.Buffer
	outW := &terst.MockDescriptor{Writer: &out, FD: 7}
	term.WriterChecker = func(int) bool { return true }
	term.GetSize = func(int) (int, int, error) { return 80, 24, nil }

	tioInst := &term.Terminal{Reader: bytes.NewBufferString(""), Writer: outW}
	stderr := tioInst.StderrWriter()

	closer, ok := stderr.(io.Closer)
	assert.True(t, ok)

	_, err := stderr.Write([]byte("flushed\n"))
	assert.NoError(t, err)

	assert.NoError(t, closer.Close())
	assert.True(t, strings.Contains(out.String(), "flushed"))
}

func TestTerminalStderrSyncFlushesWithoutClosing(t *testing.T) {
	prevTTY := term.WriterChecker
	prevSize := term.GetSize
	t.Cleanup(func() {
		term.WriterChecker = prevTTY
		term.GetSize = prevSize
	})

	var out bytes.Buffer
	outW := &terst.MockDescriptor{Writer: &out, FD: 7}
	term.WriterChecker = func(int) bool { return true }
	term.GetSize = func(int) (int, int, error) { return 80, 24, nil }

	tioInst := &term.Terminal{Reader: bytes.NewBufferString(""), Writer: outW}
	stderr := tioInst.StderrWriter()

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
