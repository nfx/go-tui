// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

type bbuf []byte

func (b *bbuf) String() string {
	return string(*b)
}

func (b *bbuf) Write(p []byte) (n int, err error) {
	*b = append(*b, p...)
	return len(p), nil
}

type tio struct {
	io.Reader
	io.Writer
}

var defaultIO = &tio{
	Reader: os.Stdin,
	Writer: os.Stderr,
}

var termGetSize = term.GetSize

func newUnstartedIO(ctx context.Context, width, height int) *chanIO {
	cio := &chanIO{
		ctx:    ctx,
		In:     make(chan string),
		Out:    make(chan string),
		vreply: make(chan chan *viewport),
		notify: make(chan viewportChanged, 1024), // buffered to avoid blocking
		width:  width,
		height: height,
	}
	cio.head = initViewport(ctx, cio.notify, cio.width, cio.height)
	cio.tail = cio.head
	return cio
}

// implements [io.ReadWriter].
type chanIO struct {
	In  chan string
	Out chan string

	ctx context.Context

	width, height int
	head, tail    *viewport
	vreply        chan chan *viewport
	notify        chan viewportChanged
}

func NewIO(ctx context.Context) (*chanIO, error) {
	w, h, err := termGetSize(int(os.Stderr.Fd()))
	if err != nil {
		return nil, fmt.Errorf("get size: %w", err)
	}
	cio := newUnstartedIO(ctx, w, h)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, os.Stderr)
	// go io.Copy(cio, os.Stdin) // FIXME: stdin forwarding is not working
	return cio, nil
}

func (i *chanIO) Read(p []byte) (n int, err error) {
	select {
	case <-i.ctx.Done():
		return 0, io.EOF
	case res, ok := <-i.In:
		if !ok {
			return 0, io.EOF
		}
		copy(p, res)
		return len(res), nil
	}
}

func (i *chanIO) Write(p []byte) (n int, err error) {
	select { // don't send on a closed channel
	case <-i.ctx.Done():
		return 0, io.EOF
	default:
	}
	select {
	case <-i.ctx.Done():
		return 0, io.EOF
	case i.Out <- string(p):
		return len(p), nil
	}
}

func (i *chanIO) pushViewport() (*viewport, error) {
	select {
	case <-i.ctx.Done():
		return nil, i.ctx.Err()
	default:
	}
	added := make(chan *viewport)
	defer close(added)
	select {
	case <-i.ctx.Done():
		return nil, i.ctx.Err()
	case i.vreply <- added:
		select {
		case <-i.ctx.Done():
			return nil, i.ctx.Err()
		default:
		}
		select {
		case <-i.ctx.Done():
			return nil, i.ctx.Err()
		case vp := <-added:
			return vp, nil
		}
	}
}

func (i *chanIO) handleViewports(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case reply := <-i.vreply:
			prev := i.head
			// TODO: height is not really relevant anymore?..
			i.head = initViewport(ctx, i.notify, i.width, i.height)
			i.head.fixedHeight = true
			i.head.next = prev
			select {
			case <-ctx.Done():
				return
			case reply <- i.head:
			}
		case line := <-i.Out:
			// [chainIO.forwardTo] will flush the actual writer
			i.tail.Write([]byte(line)) //nolint:errcheck // TODO: handle error
		}
	}
}

func (i *chanIO) forwardTo(ctx context.Context, w io.Writer) {
	var prevH, currH int
	for {
		select {
		case <-ctx.Done():
			return
		case <-i.notify:
			var buf bytes.Buffer
			if prevH > 0 { // todo: separate thread for flushing all viewports and viewports have to notify it
				space := prevH
				// Move cursor up to the beginning of the dropdown
				fmt.Fprintf(&buf, "\x1b[%dA", space)
				// Clear each line
				for i := range space {
					fmt.Fprint(&buf, "\r")     // return to start of line
					fmt.Fprint(&buf, "\x1b[K") // clear current line
					if i < space-1 {
						fmt.Fprint(&buf, "\x1b[1B") // move cursor down if not last line
					}
				}
				// Move cursor back up to the beginning and to the start of the line
				if space > 1 {
					// space-1 words well for mid scroll, but space-1 is good for screen redraw
					fmt.Fprintf(&buf, "\x1b[%dA\r", space)
				}
			}
			currH = i.head.combinedHeight()
			prevH = currH
			i.head.WriteTo(&buf) //nolint:errcheck // TODO: handle error
			x := buf.Bytes()
			w.Write(x[:len(x)-1]) //nolint:errcheck // trim last newline
			// _, _ = buf.WriteTo(w)
		}
	}
}

func newWriteC(ctx context.Context) *writeC {
	return &writeC{
		Context: ctx,
		C:       make(chan string),
	}
}

type writeC struct {
	context.Context
	C chan string
}

func (x *writeC) Write(p []byte) (n int, err error) {
	select {
	case <-x.Done():
		return 0, io.EOF
	case x.C <- string(p):
		return len(p), nil
	}
}
