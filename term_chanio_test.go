// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
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
		OpT(func(s *Spinners) error { //nolint:unparam // ..
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
	ctx, cancel := context.WithCancel(t.Context())
	var bg sync.WaitGroup
	// stop background goroutines before restoring the global they read
	t.Cleanup(func() {
		cancel()
		bg.Wait()
		termGetSize = prevSize
	})
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	// a real fd makes every refresh write the shared geometry
	cio := newUnstartedIO(ctx, 80, 24, 2)
	bg.Add(2)
	go func() { defer bg.Done(); cio.handleViewports(ctx) }()
	go func() { defer bg.Done(); cio.forwardTo(ctx, io.Discard) }()

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
