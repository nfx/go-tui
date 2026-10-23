// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestChanIOReadWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cio := &ChanIO{
		Ctx: ctx,
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
	cio := NewUnstartedIO(ctx, 10, 3, 0)
	var out syncBuffer
	go cio.ForwardTo(ctx, &out)
	_, err := cio.Head.Write([]byte("hi\n"))
	assert.NoError(t, err)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && out.Len() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	assert.True(t, out.Len() > 0)
	first := out.String()
	_, err = cio.Head.Write([]byte("there\n"))
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
	cio := NewUnstartedIO(ctx, 10, 3, 0)
	_, err := cio.PushViewport()
	assert.Error(t, err)
}

func TestChanIOPushViewportCanceledAfterSend(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cio := &ChanIO{
		Ctx:    ctx,
		In:     make(chan string),
		Out:    make(chan string),
		vreply: make(chan chan *Viewport, 1),
		notify: make(chan viewportChanged, 1),
		Width:  10,
		Height: 3,
	}
	cio.Head = initViewport(ctx, cio.notify, cio.Width, cio.Height)
	cio.tail = cio.Head
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := cio.PushViewport()
	assert.Error(t, err)
}

func TestChanIOWriteCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cio := &ChanIO{
		Ctx: ctx,
		In:  make(chan string),
		Out: make(chan string),
	}
	_, err := cio.Write([]byte("x"))
	assert.Error(t, err)
}

func TestChanIOPushViewportSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := NewUnstartedIO(ctx, 10, 3, 0)
	go cio.HandleViewports(ctx)
	vp, err := cio.PushViewport()
	assert.NoError(t, err)
	assert.NotNil(t, vp)
}

func TestChanIOPushViewportAppendsAfterExistingManagedViewport(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := NewUnstartedIO(ctx, 10, 3, 0)
	base := cio.Head
	go cio.HandleViewports(ctx)

	first, err := cio.PushViewport()
	assert.NoError(t, err)
	second, err := cio.PushViewport()
	assert.NoError(t, err)

	assert.Equal(t, first, cio.Head)
	assert.Equal(t, second, first.Next)
	assert.Equal(t, base, second.Next)
}

func TestChanIOWriteCanceledDuringSend(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cio := &ChanIO{
		Ctx: ctx,
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
	w := NewWriteC(ctx)
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
	cio := NewUnstartedIO(ctx, 80, 24, 0)
	go cio.ForwardTo(ctx, &out)

	// send messages directly to the buffered channel before cancelling,
	// so the drain path must handle them rather than the main loop.
	cio.Out <- "first\n"
	cio.Out <- "second\n"
	cancel()
	<-cio.Done // block until forwardTo has finished draining

	assert.True(t, strings.Contains(out.String(), "first"))
	assert.True(t, strings.Contains(out.String(), "second"))
}

func TestChanIOFlushDrainsBufferedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := NewUnstartedIO(ctx, 80, 24, 0)
	var out bytes.Buffer
	go cio.HandleViewports(ctx)
	go cio.ForwardTo(ctx, &out)

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
	cio := NewUnstartedIO(ctx, 10, 3, 0)
	var out bytes.Buffer
	go cio.ForwardTo(ctx, &out)

	result := make(chan error, 1)
	go func() { result <- cio.Head.WriteAndWait([]byte("hello")) }()

	select {
	case err := <-result:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("writeAndWait did not complete without context cancellation")
	}
	assert.NoError(t, ctx.Err())
}

func TestChanIOSharedGeometryRefreshIsRaceFree(t *testing.T) {
	prevSize := GetSize
	ctx, cancel := context.WithCancel(t.Context())
	var bg sync.WaitGroup
	// stop background goroutines before restoring the global they read
	t.Cleanup(func() {
		cancel()
		bg.Wait()
		GetSize = prevSize
	})
	GetSize = func(int) (int, int, error) { return 80, 24, nil }
	// a real fd makes every refresh write the shared geometry
	cio := NewUnstartedIO(ctx, 80, 24, 2)
	bg.Add(2)
	go func() { defer bg.Done(); cio.HandleViewports(ctx) }()
	go func() { defer bg.Done(); cio.ForwardTo(ctx, io.Discard) }()

	done := make(chan struct{})
	for range 2 {
		go func() {
			defer func() { done <- struct{}{} }()
			vp, err := cio.PushViewport()
			if err != nil {
				t.Error(err)
				return
			}
			w, h := cio.size()
			tio := &IO{Cio: cio, Vp: vp, Width: w, Height: h}
			for range 50 {
				tio.RefreshSize()
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
	cio := NewUnstartedIO(t.Context(), 80, 24, 0)
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
	cio := NewUnstartedIO(t.Context(), 80, 24, 0)
	n, err := cio.Read(nil)
	assert.NoError(t, err)
	assert.Equal(t, 0, n)

	go func() { cio.In <- "x" }()
	p := make([]byte, 4)
	n, err = cio.Read(p)
	assert.NoError(t, err)
	assert.Equal(t, "x", string(p[:n]))
}

func managedViewports(cio *ChanIO) int {
	cio.ChainMu.Lock()
	defer cio.ChainMu.Unlock()
	var n int
	for curr := cio.Head; curr != nil; curr = curr.Next {
		if curr.FixedHeight {
			n++
		}
	}
	return n
}

func TestChanIOReleasedViewportsDoNotAccumulate(t *testing.T) {
	cio := startChanIO(t.Context())
	var released []*Viewport
	for range 3 {
		vp, err := cio.PushViewport()
		assert.NoError(t, err)
		assert.NoError(t, vp.WriteAndWait([]byte("prompt")))
		assert.NoError(t, vp.WriteAndWait(nil))
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
	vp, err := cio.PushViewport()
	assert.NoError(t, err)
	assert.Equal(t, 1, managedViewports(cio))
	cio.releaseViewport(vp)
}

func TestChanIOReleasedViewportKeepsLastFrameInScrollback(t *testing.T) {
	cio, stdout := chainIOforTest(t, 6)
	kept, err := cio.PushViewport()
	assert.NoError(t, err)
	live, err := cio.PushViewport()
	assert.NoError(t, err)
	assert.NoError(t, kept.WriteAndWait([]byte("aborted")))
	assert.NoError(t, live.WriteAndWait([]byte("live")))
	_ = waitOutputContains(t, stdout.C, "live")

	cio.releaseViewport(kept)
	// the released frame ends its line and the live overlay is redrawn below it
	out := waitOutputContains(t, stdout.C, "\raborted\n\x1b[?7l\rlive\x1b[?7h")
	assert.True(t, !strings.HasSuffix(out, "\n"))
	assert.Equal(t, 1, managedViewports(cio))

	cio.releaseViewport(live)
	out = waitOutputContains(t, stdout.C, "\rlive\n")
	assert.True(t, strings.HasSuffix(out, "\n"))
	assert.Equal(t, 0, managedViewports(cio))
}

// failingWriter fails every write with err.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestChanIOFlushAndCloseReportWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cio := NewUnstartedIO(ctx, 80, 24, 0)
	go cio.HandleViewports(ctx)
	go cio.ForwardTo(ctx, failingWriter{io.ErrShortWrite})
	tio := &Terminal{Arbiter: cio, Cancel: cancel}

	_, err := cio.Write([]byte("lost\n"))
	assert.NoError(t, err) // queueing is asynchronous
	assert.ErrorIs(t, cio.Flush(), io.ErrShortWrite)
	assert.ErrorIs(t, tio.Flush(), io.ErrShortWrite)
	assert.ErrorIs(t, tio.Close(), io.ErrShortWrite)
}

// cutWriter is a terminal that receives the first n bytes of the first write
// before it fails, and every later write whole.
type cutWriter struct {
	got bytes.Buffer
	n   int
	cut bool
}

func (w *cutWriter) Write(p []byte) (int, error) {
	if w.cut {
		return w.got.Write(p)
	}
	w.cut = true
	n, _ := w.got.Write(p[:min(w.n, len(p))])
	return n, io.ErrShortWrite
}

func TestChanIORestoresAutowrapAfterFailedFrame(t *testing.T) {
	out := &cutWriter{n: len(AutowrapOff)}
	cio := startChanIOTo(t.Context(), out)
	vp, err := cio.PushViewport()
	assert.NoError(t, err)
	assert.NoError(t, vp.WriteAndWait([]byte("row")))
	assert.ErrorIs(t, cio.Flush(), io.ErrShortWrite)
	assert.Equal(t, AutowrapOff+AutowrapOn, out.got.String())
}

func TestIOWriteRestoresAutowrapAfterFailedFrame(t *testing.T) {
	out := &cutWriter{n: len(AutowrapOff)}
	tio := &IO{Out: out}
	_, err := tio.Write([]byte(AutowrapOff + "\rrow\n\r" + AutowrapOn))
	assert.ErrorIs(t, err, io.ErrShortWrite)
	assert.Equal(t, AutowrapOff+AutowrapOn, out.got.String())
}
