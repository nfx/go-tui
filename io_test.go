// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func chainIOforTest(t *testing.T, width, height int) (*chanIO, *writeC) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	realOut := newWriteC(ctx)
	cio := newUnstartedIO(ctx, width, height)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, realOut)
	t.Cleanup(func() {
		cancel()
		close(cio.In)
		close(cio.Out)
		close(realOut.C)
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
		spinnersOpt(func(s *Spinners) error {
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
	cio := newUnstartedIO(ctx, 10, 3)
	var out bytes.Buffer
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
	cio := newUnstartedIO(ctx, 10, 3)
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
	cio := newUnstartedIO(ctx, 10, 3)
	go cio.handleViewports(ctx)
	vp, err := cio.pushViewport()
	assert.NoError(t, err)
	assert.NotNil(t, vp)
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
