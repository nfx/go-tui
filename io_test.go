// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func chainIOforTest(t *testing.T, width, height int) (*chanIO, *writeC) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	realOut := newWriteC(ctx)
	cio := newUnstartedIO(ctx, width, height, 0)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, realOut)
	t.Cleanup(func() {
		cancel()
		close(cio.In)
		close(cio.Out)
	})
	return cio, realOut
}

func TestChanIO_Forward(t *testing.T) {
	t.SkipNow()
	cio, stdout := chainIOforTest(t, 12, 3)

	// write 5 lines
	_, err := fmt.Fprint(cio, "a\nb\nc\nd\ne\n")
	assert.NoError(t, err)

	// render 3 lines due to the viewport height
	assert.Equal(t, "\rc           \n\rd           \n\re           ", <-stdout.C)

	// write one more line
	_, err = fmt.Fprint(cio, "f\n")
	assert.NoError(t, err)

	// and have the previous two lines still rendered
	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rd           \n\re           \n\rf           ",
		<-stdout.C)

	ticks := make(chan time.Time)
	defer close(ticks)
	tick := func() {
		go func() {
			select {
			case <-cio.ctx.Done():
			case ticks <- time.Now():
			}
		}()
	}

	s, err := NewSpinners(cio.ctx,
		WithOutput(cio),
		opT(func(s *Spinners) error { //nolint:unparam // ..
			s.ticks = ticks
			return nil
		}),
	)
	assert.NoError(t, err)

	s.MustAddBackground().Update("s: A")
	tick()

	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\r\r... s: A   \n\r\r           \n\rf           ",
		<-stdout.C)

	// write one more line
	_, err = fmt.Fprint(cio, "g\n")
	assert.NoError(t, err)

	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\r\r... s: A   \n\r\r           \n\rg           ",
		<-stdout.C)
}

func TestNewIOReturnsOrErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err := NewIO(ctx)
	if err != nil {
		return
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

func TestSpinnersCloseNil(t *testing.T) {
	var s *Spinners
	s.Close()
}

func TestChanIOReadWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string, 1),
		Out: make(chan string, 1),
	}

	msg := "hello"
	n, err := cio.Write([]byte(msg))
	assert.NoError(t, err)
	assert.Equal(t, len(msg), n)
	select {
	case got := <-cio.Out:
		assert.Equal(t, msg, got)
	default:
		t.Fatalf("expected message on Out")
	}

	cio.In <- "read"
	buf := make([]byte, 10)
	n, err = cio.Read(buf)
	assert.NoError(t, err)
	assert.Equal(t, "read", string(buf[:n]))
}

func TestChanIOForwardToWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := newUnstartedIO(ctx, 10, 3, 0)
	var out syncBuffer
	go cio.forwardTo(ctx, &out)
	_, err := cio.head.Write([]byte("hi\n"))
	assert.NoError(t, err)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && out.Len() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	assert.True(t, out.Len() > 0)
	first := out.String()
	_, err = cio.head.Write([]byte("there\n"))
	assert.NoError(t, err)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && out.Len() == len(first) {
		time.Sleep(10 * time.Millisecond)
	}
	assert.True(t, out.Len() > len(first))
}

func TestChanIOPushViewportCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cio := newUnstartedIO(ctx, 10, 3, 0)
	_, err := cio.pushViewport()
	assert.Error(t, err)
}

func TestChanIOPushViewportCanceledAfterSend(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cio := &chanIO{
		ctx:    ctx,
		In:     make(chan string),
		Out:    make(chan string),
		vreply: make(chan chan *viewport, 1),
		notify: make(chan viewportChanged, 1),
		width:  10,
		height: 3,
	}
	cio.head = initViewport(ctx, cio.notify, cio.width, cio.height)
	cio.tail = cio.head
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := cio.pushViewport()
	assert.Error(t, err)
}

func TestChanIOWriteCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string),
	}
	_, err := cio.Write([]byte("x"))
	assert.Error(t, err)
}

func TestChanIOPushViewportSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := newUnstartedIO(ctx, 10, 3, 0)
	go cio.handleViewports(ctx)
	vp, err := cio.pushViewport()
	assert.NoError(t, err)
	assert.NotNil(t, vp)
}

func TestChanIOPushViewportAppendsAfterExistingManagedViewport(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := newUnstartedIO(ctx, 10, 3, 0)
	base := cio.head
	go cio.handleViewports(ctx)

	first, err := cio.pushViewport()
	assert.NoError(t, err)
	second, err := cio.pushViewport()
	assert.NoError(t, err)

	assert.Equal(t, first, cio.head)
	assert.Equal(t, second, first.next)
	assert.Equal(t, base, second.next)
}

func TestChanIOWriteCanceledDuringSend(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: nil,
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := cio.Write([]byte("x"))
	assert.Error(t, err)
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
	assert.Equal(t, in, input.in)
	assert.Equal(t, out, input.out)

	s := newSpinners(t.Context())
	assert.Equal(t, in, s.in)
	assert.Equal(t, out, s.out)
	s.ticker.Stop()

	p := newProgressbar(t.Context())
	assert.Equal(t, in, p.in)
	assert.Equal(t, out, p.out)
	p.ticker.Stop()
}

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
	assert.Equal(t, in, input.in)
	assert.Equal(t, cio, input.out)

	s := newSpinners(t.Context())
	assert.Equal(t, in, s.in)
	assert.Equal(t, cio, s.out)
	s.ticker.Stop()

	p := newProgressbar(t.Context())
	assert.Equal(t, in, p.in)
	assert.Equal(t, cio, p.out)
	p.ticker.Stop()
}

func TestWriteCWritesToChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := newWriteC(ctx)
	message := "hello"
	go func() {
		_, err := w.Write([]byte(message))
		assert.NoError(t, err)
	}()
	select {
	case got := <-w.C:
		assert.Equal(t, message, got)
	case <-ctx.Done():
		t.Fatalf("context canceled unexpectedly")
	}
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

	initial := waitOutputContains(t, stdout.C, "Label")
	updated := waitOutputContains(t, stdout.C, "Label", "hi")
	assert.True(t, initial != updated)
	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "hi", value)
	}
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

func TestChanIOForwardToDrainsPendingOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var out bytes.Buffer
	cio := newUnstartedIO(ctx, 80, 24, 0)
	go cio.forwardTo(ctx, &out)

	// send messages directly to the buffered channel before cancelling,
	// so the drain path must handle them rather than the main loop.
	cio.Out <- "first\n"
	cio.Out <- "second\n"
	cancel()
	<-cio.done // block until forwardTo has finished draining

	assert.True(t, strings.Contains(out.String(), "first"))
	assert.True(t, strings.Contains(out.String(), "second"))
}

func TestTioCloseFlushesOutput(t *testing.T) {
	prevTTY := terminalWriterChecker
	prevSize := termGetSize
	t.Cleanup(func() {
		terminalWriterChecker = prevTTY
		termGetSize = prevSize
	})

	var out bytes.Buffer
	outW := &mockDescriptor{Writer: &out, fd: 42}
	terminalWriterChecker = func(int) bool { return true }
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }

	tioInst := &tio{reader: bytes.NewBufferString(""), writer: outW}
	w := tioInst.stderrWriter()

	_, err := w.Write([]byte("pending output\n"))
	assert.NoError(t, err)

	assert.NoError(t, tioInst.Close())
	assert.True(t, strings.Contains(out.String(), "pending output"))
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

func TestChanIOFlushDrainsBufferedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := newUnstartedIO(ctx, 80, 24, 0)
	var out bytes.Buffer
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, &out)

	_, err := cio.Write([]byte("flushed\n"))
	assert.NoError(t, err)
	assert.NoError(t, cio.Flush())
	assert.True(t, strings.Contains(out.String(), "flushed"))

	// arbiter still running — second write must succeed after flush.
	_, err = cio.Write([]byte("after flush\n"))
	assert.NoError(t, err)
	assert.NoError(t, cio.Flush())
	assert.True(t, strings.Contains(out.String(), "after flush"))
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

func waitOutputContains(t *testing.T, out <-chan string, parts ...string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-out:
			matched := true
			for _, part := range parts {
				if !strings.Contains(got, part) {
					matched = false
					break
				}
			}
			if matched {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for output containing %q", parts)
		}
	}
}

func TestChanIOWriteAndWaitCompletesWithoutCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := newUnstartedIO(ctx, 10, 3, 0)
	var out bytes.Buffer
	go cio.forwardTo(ctx, &out)

	result := make(chan error, 1)
	go func() { result <- cio.head.writeAndWait([]byte("hello")) }()

	select {
	case err := <-result:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("writeAndWait did not complete without context cancellation")
	}
	assert.NoError(t, ctx.Err())
}

func TestChanIOSharedGeometryRefreshIsRaceFree(t *testing.T) {
	prevSize := termGetSize
	t.Cleanup(func() { termGetSize = prevSize })
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	ctx := t.Context()
	// a real fd makes every refresh write the shared geometry
	cio := newUnstartedIO(ctx, 80, 24, 2)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, io.Discard)

	done := make(chan struct{})
	for range 2 {
		go func() {
			defer func() { done <- struct{}{} }()
			vp, err := cio.pushViewport()
			if err != nil {
				t.Error(err)
				return
			}
			w, h := cio.size()
			tio := &termIO{cio: cio, vp: vp, Width: w, Height: h}
			for range 50 {
				tio.refreshSize()
				if _, err := tio.Write([]byte("frame")); err != nil {
					t.Error(err)
					return
				}
			}
			tio.release()
		}()
	}
	<-done
	<-done
	w, h := cio.size()
	assert.Equal(t, 80, w)
	assert.Equal(t, 24, h)
}

func TestChanIOReadKeepsBytesThatDoNotFit(t *testing.T) {
	cio := newUnstartedIO(t.Context(), 80, 24, 0)
	go func() { cio.In <- "abcdef" }()

	var got []string
	for range 3 {
		p := make([]byte, 2)
		n, err := cio.Read(p)
		assert.NoError(t, err)
		assert.Equal(t, 2, n)
		got = append(got, string(p[:n]))
	}
	assert.Equal(t, []string{"ab", "cd", "ef"}, got)
}

func TestChanIOZeroLengthReadDoesNotConsumeInput(t *testing.T) {
	cio := newUnstartedIO(t.Context(), 80, 24, 0)
	n, err := cio.Read(nil)
	assert.NoError(t, err)
	assert.Equal(t, 0, n)

	go func() { cio.In <- "x" }()
	p := make([]byte, 4)
	n, err = cio.Read(p)
	assert.NoError(t, err)
	assert.Equal(t, "x", string(p[:n]))
}

func managedViewports(cio *chanIO) int {
	cio.chainMu.Lock()
	defer cio.chainMu.Unlock()
	var n int
	for curr := cio.head; curr != nil; curr = curr.next {
		if curr.fixedHeight {
			n++
		}
	}
	return n
}

func TestChanIOReleasedViewportsDoNotAccumulate(t *testing.T) {
	cio := startChanIO(t.Context(), 80, 24)
	var released []*viewport
	for range 3 {
		vp, err := cio.pushViewport()
		assert.NoError(t, err)
		assert.NoError(t, vp.writeAndWait([]byte("prompt")))
		assert.NoError(t, vp.writeAndWait(nil))
		cio.releaseViewport(vp)
		cio.releaseViewport(vp) // idempotent
		released = append(released, vp)
	}
	assert.Equal(t, 0, managedViewports(cio))
	for _, vp := range released {
		// the viewport loop stops with its context
		assert.NotNil(t, vp.ctx.Err())
	}
	// the chain still accepts new prompts
	vp, err := cio.pushViewport()
	assert.NoError(t, err)
	assert.Equal(t, 1, managedViewports(cio))
	cio.releaseViewport(vp)
}

func TestChanIOReleasedViewportKeepsLastFrameInScrollback(t *testing.T) {
	cio, stdout := chainIOforTest(t, 40, 6)
	kept, err := cio.pushViewport()
	assert.NoError(t, err)
	live, err := cio.pushViewport()
	assert.NoError(t, err)
	assert.NoError(t, kept.writeAndWait([]byte("aborted")))
	assert.NoError(t, live.writeAndWait([]byte("live")))
	_ = waitOutputContains(t, stdout.C, "live")

	cio.releaseViewport(kept)
	// the released frame ends its line and the live overlay is redrawn below it
	out := waitOutputContains(t, stdout.C, "\raborted\n\rlive")
	assert.True(t, !strings.HasSuffix(out, "\n"))
	assert.Equal(t, 1, managedViewports(cio))

	cio.releaseViewport(live)
	out = waitOutputContains(t, stdout.C, "\rlive\n")
	assert.True(t, strings.HasSuffix(out, "\n"))
	assert.Equal(t, 0, managedViewports(cio))
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestChanIOFlushAndCloseReportWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cio := newUnstartedIO(ctx, 80, 24, 0)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, failingWriter{io.ErrShortWrite})
	tio := &tio{arbiter: cio, cancel: cancel}

	_, err := cio.Write([]byte("lost\n"))
	assert.NoError(t, err) // queueing is asynchronous
	assert.ErrorIs(t, cio.Flush(), io.ErrShortWrite)
	assert.ErrorIs(t, tio.Flush(), io.ErrShortWrite)
	assert.ErrorIs(t, tio.Close(), io.ErrShortWrite)
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

var regexpSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// withEnv installs an output environment fixture built from KEY=VALUE pairs.
func withEnv(t *testing.T, pairs ...string) {
	t.Helper()
	vars := map[string]string{}
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		vars[k] = v
	}
	prev := defaultIO.env
	defaultIO.env = readOutputEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
	t.Cleanup(func() { defaultIO.env = prev })
}

func ttyWriter(t *testing.T) *mockDescriptor {
	t.Helper()
	prev := terminalWriterChecker
	terminalWriterChecker = func(int) bool { return true }
	prevVT := consoleVT
	consoleVT = func(io.Writer) bool { return true }
	t.Cleanup(func() {
		terminalWriterChecker = prev
		consoleVT = prevVT
	})
	return &mockDescriptor{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}, fd: 7}
}

func TestProfileFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		tty  bool
		want colorProfile
	}{
		{"tty truecolor", []string{"COLORTERM=truecolor"}, true, profileTrueColor},
		{"tty 24bit", []string{"COLORTERM=24bit"}, true, profileTrueColor},
		{"tty 256", []string{"TERM=xterm-256color"}, true, profileANSI256},
		{"tty bare", nil, true, profileANSI16},
		{"tty windows terminal", []string{"WT_SESSION=x"}, true, profileTrueColor},
		{"pipe force 3", []string{"FORCE_COLOR=3"}, false, profileTrueColor},
		{"pipe force 2", []string{"FORCE_COLOR=2"}, false, profileANSI256},
		{"pipe force 1", []string{"FORCE_COLOR=1"}, false, profileANSI16},
		{"pipe force empty", []string{"FORCE_COLOR="}, false, profileANSI16},
		{"pipe force garbage", []string{"FORCE_COLOR=banana"}, false, profileANSI16},
		{"tty force 0", []string{"FORCE_COLOR=0"}, true, profilePlain},
		{"tty force false", []string{"FORCE_COLOR=false"}, true, profilePlain},
		{"unknown non-tty", nil, false, profilePlain},
		{"clicolor force", []string{"CLICOLOR_FORCE=1"}, false, profileANSI16},
		{"clicolor force 0", []string{"CLICOLOR_FORCE=0"}, false, profilePlain},
		{"clicolor force depth", []string{"CLICOLOR_FORCE=1", "COLORTERM=truecolor"}, false, profileTrueColor},
		{"no color tty", []string{"NO_COLOR=1"}, true, profileAttrs},
		{"no color pipe", []string{"NO_COLOR=1"}, false, profilePlain},
		{"force beats no color", []string{"NO_COLOR=1", "FORCE_COLOR=3"}, true, profileTrueColor},
		{"dumb", []string{"TERM=dumb"}, true, profilePlain},
		{"ci tty", []string{"CI=true", "COLORTERM=truecolor"}, true, profileAttrs},
		{"ci pipe", []string{"CI=true"}, false, profilePlain},
		{"ci 1", []string{"CI=1"}, true, profileAttrs},
		{"ci false", []string{"CI=false"}, true, profileANSI16},
		{"ci 0", []string{"CI=0"}, true, profileANSI16},
		{"ci clicolor force", []string{"CI=true", "CLICOLOR_FORCE=1"}, false, profileANSI16},
		{"ci force color", []string{"CI=true", "FORCE_COLOR=3"}, false, profileTrueColor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withEnv(t, tc.env...)
			var w io.Writer = &bytes.Buffer{}
			if tc.tty {
				w = ttyWriter(t)
			}
			assert.Equal(t, tc.want, defaultIO.profileFor(w))
		})
	}
}

func TestProfileConsoleWithoutVTIsPlain(t *testing.T) {
	withEnv(t, "COLORTERM=truecolor")
	tty := ttyWriter(t)
	consoleVT = func(io.Writer) bool { return false }
	assert.Equal(t, profilePlain, defaultIO.profileFor(tty))
}

func TestProfileConsoleVTNotCalledForPipe(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	prev := consoleVT
	consoleVT = func(io.Writer) bool {
		t.Fatal("pipe must not reach SetConsoleMode")
		return false
	}
	t.Cleanup(func() { consoleVT = prev })
	assert.Equal(t, profileTrueColor, defaultIO.profileFor(&bytes.Buffer{}))
}

func TestProfileBold(t *testing.T) {
	assert.Equal(t, "x", profilePlain.bold("x"))
	assert.Equal(t, "\x1b[1mx\x1b[0m", profileAttrs.bold("x"))
	assert.Equal(t, "\x1b[1mx\x1b[0m", profileTrueColor.bold("x"))
}

func TestProfileForManagedWriterUsesRawOutput(t *testing.T) {
	withEnv(t, "COLORTERM=truecolor")
	tty := ttyWriter(t)
	managed := &tio{writer: tty, env: defaultIO.env}
	assert.Equal(t, profileTrueColor, managed.profileFor(&chanIO{}))
	piped := &tio{writer: &bytes.Buffer{}, env: defaultIO.env}
	assert.Equal(t, profilePlain, piped.profileFor(&chanIO{}))
}

func TestTableHeadersFollowProfile(t *testing.T) {
	type row struct{ Name string }
	for _, tc := range []struct {
		env  []string
		bold bool
	}{{nil, false}, {[]string{"FORCE_COLOR=1"}, true}} {
		withEnv(t, tc.env...)
		var out bytes.Buffer
		assert.NoError(t, TableAuto(&out, []row{{"a"}}))
		assert.Equal(t, tc.bold, strings.Contains(out.String(), "\x1b[1m"))
	}
}

func TestFactsFollowProfile(t *testing.T) {
	type row struct{ Name string }
	withEnv(t)
	var out bytes.Buffer
	assert.NoError(t, Facts(&out, row{"a"}))
	assert.NotContains(t, out.String(), "\x1b[")
	withEnv(t, "FORCE_COLOR=1")
	out.Reset()
	assert.NoError(t, Facts(&out, row{"a"}))
	assert.Contains(t, out.String(), "\x1b[1m")
}

func TestPrettyJSONLevels(t *testing.T) {
	render := func(env string) string { return renderJSON(t, env, `{"a":{"b":"c"}}`) }
	assert.NotContains(t, render("FORCE_COLOR=0"), "\x1b[")
	attrs := render("FORCE_COLOR=1")
	assert.True(t, regexp.MustCompile("\x1b\\[(3|9)[0-7]m").MatchString(attrs))
	assert.NotContains(t, attrs, "38;5;")
	assert.Contains(t, render("FORCE_COLOR=2"), "38;5;")
	assert.Contains(t, render("FORCE_COLOR=3"), "38;2;")
	stripped := regexpSGR.ReplaceAllString(render("FORCE_COLOR=1"), "")
	assert.Equal(t, "{\n  \"a\": {\n    \"b\": \"c\"\n  }\n}\n", stripped)
}

func TestGeometryValueColumns(t *testing.T) {
	long := text(strings.Repeat("x", 100))
	for _, tc := range []struct {
		name     string
		inline   bool
		guide    string
		width    int
		label    text
		wantLead string
		wantCols int
		wantErr  bool
	}{
		{"guide unknown width", false, "│", 0, text("Name: "), "│ ", math.MaxInt, false},
		{"inline unknown width", true, "│", 0, text("Name: "), "Name: ", math.MaxInt, false},
		{"guide width 1", false, "│", 1, nil, "│ ", 0, true},
		{"guide width 2", false, "│", 2, nil, "│ ", 0, true},
		{"guide width 3 holds only the caret", false, "│", 3, nil, "│ ", 0, true},
		{"guide width 4", false, "│", 4, nil, "│ ", 1, false},
		{"guide label does not count", false, "│", 80, long, "│ ", 77, false},
		{"ascii guide", false, "|", 10, nil, "| ", 7, false},
		{"inline width 1", true, "│", 1, text("Name: "), "", 0, true},
		{"inline width 2 drops the label", true, "│", 2, text("Name: "), "", 1, false},
		{"inline label fits exactly", true, "│", 8, text("Name: "), "Name: ", 1, false},
		{"inline long label is clipped", true, "│", 7, text("Name: "), "Name…", 1, false},
		{"inline empty label", true, "│", 10, nil, "", 9, false},
		{"inline wide label", true, "│", 6, text("\u754c\u9762\u754c: "), "\u754c…", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := geometry{inline: tc.inline, guide: tc.guide, width: tc.width, height: 1}
			lead := g.valueLead(tc.label)
			assert.Equal(t, tc.wantLead, lead.String())
			cols, err := g.valueColumns(lead)
			if tc.wantErr {
				assert.ErrorIs(t, err, ErrNoSpace)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantCols, cols)
			if tc.width > 0 {
				// the window and its caret cell fill the row exactly
				assert.Equal(t, tc.width, lead.width()+cols+1)
			}
		})
	}
}

func TestGeometryFit(t *testing.T) {
	type want struct {
		lines []string
		at    []int
	}
	for _, tc := range []struct {
		name          string
		inline        bool
		width, height int
		rows          []frameRow
		want          want
		wantErr       bool
	}{
		{"unknown size keeps everything", false, 0, 0, []frameRow{
			{content: text("label")},
			{content: text(strings.Repeat("v", 300)), guided: true},
			{content: text("hint"), guided: true, drop: 1},
		}, want{[]string{"label", "│ " + strings.Repeat("v", 300), "│ hint"}, []int{0, 1, 2}}, false},
		{"height 1 keeps the required row", false, 20, 1, []frameRow{
			{content: text("v")},
			{content: text("hint"), guided: true, drop: 1},
		}, want{[]string{"v"}, []int{0, -1}}, false},
		{"required rows exceed the height", false, 20, 1, []frameRow{
			{content: text("a")}, {content: text("b")},
		}, want{}, true},
		{"largest drop goes first", false, 20, 3, []frameRow{
			{content: text("value")},
			{content: text("details"), drop: 1},
			{content: text("more"), drop: 2},
			{content: text("hint"), drop: 3},
		}, want{[]string{"value", "details", "more"}, []int{0, 1, 2, -1}}, false},
		{"short height drops more", false, 20, 2, []frameRow{
			{content: text("value")},
			{content: text("details"), drop: 1},
			{content: text("more"), drop: 2},
			{content: text("hint"), drop: 3},
		}, want{[]string{"value", "details"}, []int{0, 1, -1, -1}}, false},
		{"equal drop removes the later row", false, 20, 2, []frameRow{
			{content: text("value")},
			{content: text("a"), drop: 1},
			{content: text("b"), drop: 1},
		}, want{[]string{"value", "a"}, []int{0, 1, -1}}, false},
		{"empty optional rows reserve nothing", false, 10, 0, []frameRow{
			{guided: true},
			{drop: 1},
			{content: text("\x1b[2m\x1b[0m"), drop: 1},
			{content: text("x")},
		}, want{[]string{"│ ", "x"}, []int{0, -1, -1, 1}}, false},
		{"exact fit is unchanged and one more is clipped", false, 10, 0, []frameRow{
			{content: text("\x1b[1mabcdefg\x1b[0m"), guided: true},
			{content: text("abcdefgh"), guided: true},
		}, want{[]string{"│ \x1b[1mabcdefg\x1b[0m", "│ abcdef…"}, []int{0, 1}}, false},
		{"wide, combining and emoji clusters", false, 6, 0, []frameRow{
			{content: text("\u754c\u9762\u754c")},
			{content: text(strings.Repeat("e\u0301", 5))},
			{content: text("👍👍👍")},
		}, want{[]string{"\u754c\u9762…", strings.Repeat("e\u0301", 5), "👍👍…"}, []int{0, 1, 2}}, false},
		{"inline has no guide", true, 10, 0, []frameRow{
			{content: text("abc"), guided: true},
		}, want{[]string{"abc"}, []int{0}}, false},
		{"width 2 cannot hold the guide", false, 2, 0, []frameRow{
			{guided: true},
		}, want{}, true},
		{"width 3 keeps an empty guided row", false, 3, 0, []frameRow{
			{guided: true},
			{content: text("hint"), guided: true, drop: 1},
		}, want{[]string{"│ "}, []int{0, -1}}, false},
		{"width 3 cannot show guided content", false, 3, 0, []frameRow{
			{content: text("x"), guided: true},
		}, want{}, true},
		{"width 1 cannot show content", true, 1, 0, []frameRow{
			{content: text("x")},
		}, want{}, true},
		{"width 2 shows the ellipsis", true, 2, 0, []frameRow{
			{content: text("xy")},
		}, want{[]string{"…"}, []int{0}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := geometry{inline: tc.inline, guide: "│", width: tc.width, height: tc.height}
			got, err := g.fit(tc.rows)
			if tc.wantErr {
				assert.ErrorIs(t, err, ErrNoSpace)
				return
			}
			assert.NoError(t, err)
			lines := []string{}
			for _, l := range got.lines {
				lines = append(lines, l.String())
				if tc.width > 0 {
					assert.True(t, l.width() < tc.width)
				}
			}
			assert.Equal(t, tc.want.lines, lines)
			assert.Equal(t, tc.want.at, got.at)
		})
	}
}

func TestFitDoesNotAliasRows(t *testing.T) {
	content := text("abc")
	g := geometry{guide: "│", width: 10}
	got, err := g.fit([]frameRow{{content: content, guided: true}})
	assert.NoError(t, err)
	got.lines[0][len(got.lines[0])-1] = 'X'
	assert.Equal(t, "abc", content.String())
}
