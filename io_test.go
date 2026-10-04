// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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

	s, err := NewSpinners(
		WithOutput(cio),
		WithContext(cio.ctx),
		spinnersOpt(func(s *Spinners) error { //nolint:unparam // ..
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

	d := newDropdown()
	assert.Equal(t, in, d.in)
	assert.Equal(t, out, d.out)

	input := newInput("label")
	assert.Equal(t, in, input.in)
	assert.Equal(t, out, input.out)

	multichoice := newMultichoice()
	assert.Equal(t, in, multichoice.in)
	assert.Equal(t, out, multichoice.out)

	s := newSpinners()
	assert.Equal(t, in, s.in)
	assert.Equal(t, out, s.out)
	s.ticker.Stop()

	p := newProgressbar()
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
	assert.Equal(t, out, defaultOutput())
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
	cio, ok := defaultOutput().(*chanIO)
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

	cio, ok := defaultOutput().(*chanIO)
	assert.True(t, ok)
	assert.NotNil(t, cio)

	d := newDropdown()
	assert.Equal(t, in, d.in)
	assert.Equal(t, cio, d.out)

	input := newInput("label")
	assert.Equal(t, in, input.in)
	assert.Equal(t, cio, input.out)

	multichoice := newMultichoice()
	assert.Equal(t, in, multichoice.in)
	assert.Equal(t, cio, multichoice.out)

	s := newSpinners()
	assert.Equal(t, in, s.in)
	assert.Equal(t, cio, s.out)
	s.ticker.Stop()

	p := newProgressbar()
	assert.Equal(t, in, p.in)
	assert.Equal(t, cio, p.out)
	p.ticker.Stop()
}

func TestBBufWrite(t *testing.T) {
	var b bbuf
	n, err := b.Write([]byte("abc"))
	assert.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, "abc", string(b))
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
		result <- Confirm("Are you sure?")
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
		result <- Confirm("Are you sure?")
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
		value, err := Input("Label")
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
		value, err := Input("PROMPT")
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
		value, err := Input("PROMPT")
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
		value, err := Input("PROMPT", WithDefault("apple"))
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
		done <- Confirm("scrollback")
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
