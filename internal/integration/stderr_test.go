// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/tcommon"
	"github.com/nfx/go-tui/internal/term"
	"io"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/terst"
)

func TestStderrReturnsRawWriterWithoutTTY(t *testing.T) {
	in := bytes.NewBufferString("in")
	out := &bytes.Buffer{}
	tcommon.Swap(t, &term.WriterChecker, func(int) bool { return false })
	terst.UseIO(t, in, out)

	assert.Equal(t, out, tui.Stderr())
	_, defaultOut := term.DefaultStreams(t.Context())
	assert.Equal(t, out, defaultOut)
}

func TestStderrReturnsCoordinatedWriterOnTTY(t *testing.T) {
	in := bytes.NewBufferString("in")
	out := &tcommon.MockDescriptor{Writer: &bytes.Buffer{}, FD: 42}
	tcommon.Swap(t, &term.WriterChecker, func(int) bool { return true })
	tcommon.Swap(t, &term.GetSize, func(int) (int, int, error) { return 80, 24, nil })
	terst.UseIO(t, in, out)

	stderr := tui.Stderr()
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
	terst.WithEnv(t, "CI=true")
	out := tcommon.TTYWriter(t)
	tcommon.Swap(t, &term.GetSize, func(int) (int, int, error) { return 80, 24, nil })
	terst.UseIO(t, bytes.NewBufferString("in"), out)

	assert.Equal(t, out, tui.Stderr())
	_, defaultOut := term.DefaultStreams(t.Context())
	assert.Equal(t, out, defaultOut)
	assert.True(t, term.Default.Arbiter == nil)
}

func TestConfirmAndStderrShareDefaultTerminal(t *testing.T) {
	keys, stdout := terst.UseTerminal(t)

	result := make(chan bool, 1)
	go func() {
		result <- tui.Confirm(t.Context(), "Are you sure?")
	}()

	stdout.AwaitOutput("Are you sure?")
	_, err := tui.Stderr().Write([]byte("INF foo bar=baz\n"))
	assert.NoError(t, err)
	stdout.AwaitOutput("INF foo bar=baz", "Are you sure?")
	keys.Send(terst.KeyEnter)
	assert.True(t, <-result)
	answer := stdout.AwaitOutput("✔", "Are you sure?")
	assert.Equal(t, 0, strings.Count(answer, "→"))
}

func TestInputAndStderrShareDefaultTerminal(t *testing.T) {
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
	_, err := tui.Stderr().Write([]byte("INF background\n"))
	assert.NoError(t, err)
	logged := stdout.AwaitOutput("PROMPT", "INF background")
	assert.Equal(t, 1, strings.Count(logged, "PROMPT"))
	reader.Send("a", "b")
	updated := stdout.AwaitOutput("PROMPT", "ab")
	assert.Equal(t, 1, strings.Count(updated, "PROMPT"))
	reader.Send(terst.KeyEnter)
	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "ab", value)
	}
}

func TestStderrWritesUseNativeHistoryWhilePromptActive(t *testing.T) {
	keys, stdout := terst.UseTerminal(t)

	done := make(chan bool, 1)
	go func() {
		done <- tui.Confirm(t.Context(), "scrollback")
	}()

	stdout.AwaitOutput("scrollback")
	_, err := tui.Stderr().Write([]byte("INF first\n"))
	assert.NoError(t, err)
	first := stdout.AwaitOutput("INF first", "scrollback")
	assert.Equal(t, 1, strings.Count(first, "INF first"))

	_, err = tui.Stderr().Write([]byte("INF second\n"))
	assert.NoError(t, err)
	second := stdout.AwaitOutput("INF second", "scrollback")
	assert.Equal(t, 0, strings.Count(second, "INF first"))
	assert.Equal(t, 1, strings.Count(second, "INF second"))

	keys.Send(terst.KeyEnter)
	assert.True(t, <-done)
}

func TestStderrNormalizesLoneLFWithoutOverlay(t *testing.T) {
	stdout := terst.UseTerminalInput(t, nil)

	_, err := tui.Stderr().Write([]byte("one\ntwo\n"))
	assert.NoError(t, err)
	assert.Equal(t, "one\r\ntwo\r\n", stdout.Next())
}

func TestStderrPreservesCRLFAcrossChunkBoundary(t *testing.T) {
	stdout := terst.UseTerminalInput(t, nil)

	_, err := tui.Stderr().Write([]byte("one\r"))
	assert.NoError(t, err)
	assert.Equal(t, "one\r", stdout.Next())

	_, err = tui.Stderr().Write([]byte("\ntwo\n"))
	assert.NoError(t, err)
	assert.Equal(t, "\ntwo\r\n", stdout.Next())
}

func TestTerminalStderrImplementsCloser(t *testing.T) {
	var out bytes.Buffer
	outW := &tcommon.MockDescriptor{Writer: &out, FD: 7}
	tcommon.Swap(t, &term.WriterChecker, func(int) bool { return true })
	tcommon.Swap(t, &term.GetSize, func(int) (int, int, error) { return 80, 24, nil })

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
	var out bytes.Buffer
	outW := &tcommon.MockDescriptor{Writer: &out, FD: 7}
	tcommon.Swap(t, &term.WriterChecker, func(int) bool { return true })
	tcommon.Swap(t, &term.GetSize, func(int) (int, int, error) { return 80, 24, nil })

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
