// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
)

type viewportChanged struct {
	lines int
	done  chan struct{}
}

type writeToResponse struct {
	bytes  int64
	lines  int
	widths []int
	err    error
}

type writeTo struct {
	io.Writer
	res chan writeToResponse
}

type viewport struct {
	width, height int
	lines         [][]byte
	next          *viewport
	inner         chan viewportWrite
	notify        chan viewportChanged
	writeTos      chan *writeTo
	ctx           context.Context
	fixedHeight   bool
	lastLines     int
}

type viewportWrite struct {
	chunk []byte
	done  chan struct{}
	width int // if positive, update viewport width before processing
}

// initViewport allocates a viewport with the given
// dimensions and starts its event loop goroutine.
func initViewport(ctx context.Context, notify chan viewportChanged, width, height int) *viewport {
	v := &viewport{
		ctx:      ctx,
		inner:    make(chan viewportWrite),
		writeTos: make(chan *writeTo),
		notify:   notify,
		width:    width,
		height:   height,
	}
	go v.loop()
	return v
}

// WriteTo walks the viewport chain and renders each
// viewport into w within the height budget.
func (v *viewport) WriteTo(w io.Writer) (int64, error) {
	var total int64
	curr := v
	budget := v.height
	for curr != nil {
		respond := make(chan writeToResponse)
		select {
		case <-curr.ctx.Done():
			return total, io.EOF
		// [viewport.loop] will handle the write
		case curr.writeTos <- &writeTo{Writer: w, res: respond}:
			select {
			case <-curr.ctx.Done():
				return total, io.EOF
			// [viewport.loop] will handle the response
			case res := <-respond:
				close(respond)
				if res.err != nil {
					return total, res.err
				}
				total += res.bytes
				budget -= res.lines
				if budget <= 0 {
					return total, nil
				}
				curr = curr.next
				if curr != nil {
					// simplified assumption: tail viewport cannot have fixed height
					curr.height = budget
				}
			}
		}
	}
	return total, nil
}

func (v *viewport) WriteByte(b byte) error {
	_, err := v.Write([]byte{b})
	return err
}

// see https://notes.burke.libbey.me/ansi-escape-codes/
// see https://gist.github.com/fnky/458719343aabd01cfb17a3a4f7296797
func (v *viewport) Write(chunk []byte) (n int, err error) {
	return v.write(chunk, nil)
}

// write sends a chunk to the viewport's event loop,
// optionally signaling done after the arbiter applies it.
func (v *viewport) write(chunk []byte, done chan struct{}) (n int, err error) {
	select {
	case <-v.ctx.Done():
		return 0, io.EOF
	// [viewport.loop] will handle the write, hence the copy
	case v.inner <- viewportWrite{chunk: bytes.Clone(chunk), done: done}:
		return len(chunk), nil
	}
}

// writeWithWidth sends a chunk and updates the viewport width
// atomically before line wrapping, used by widget renders.
func (v *viewport) writeWithWidth(chunk []byte, width int) (n int, err error) {
	select {
	case <-v.ctx.Done():
		return 0, io.EOF
	case v.inner <- viewportWrite{chunk: bytes.Clone(chunk), width: width}:
		return len(chunk), nil
	}
}

// writeAndWait blocks until the arbiter has applied this viewport update.
func (v *viewport) writeAndWait(chunk []byte) error {
	done := make(chan struct{})
	if _, err := v.write(chunk, done); err != nil {
		return err
	}
	select {
	case <-v.ctx.Done():
		return io.EOF
	case <-done:
		return nil
	}
}

// combinedHeight sums the height of this viewport
// and all linked viewports in the chain.
func (v *viewport) combinedHeight() int {
	var n int
	curr := v
	for curr != nil {
		n += curr.height
		curr = curr.next
	}
	return n
}

// numLines counts the total buffered lines across
// this viewport and all linked viewports.
func (v *viewport) numLines() int {
	var n int
	curr := v
	for curr != nil {
		n += len(curr.lines)
		curr = curr.next
	}
	return n
}

// writeTo writes the viewport to the given writer, called from [viewport.loop], which
// confines all mutability to a single goroutine.
func (v *viewport) writeTo(w io.Writer) (int64, []int, error) {
	if v.fixedHeight {
		if v.lastLines == 0 {
			v.lines = [][]byte{}
		} else {
			v.lines = v.lines[len(v.lines)-v.lastLines:]
		}
	} else if len(v.lines) > v.height {
		// TODO: definitely need two offsets, as the top fixed viewport will be the first to be trimmed
		v.lines = v.lines[len(v.lines)-v.height:]
	}
	var bytes int64
	widths := make([]int, 0, len(v.lines))
	for _, l := range v.lines {
		b, err := fmt.Fprintf(w, "\r%s", l)
		if err != nil {
			return bytes, widths, err
		}
		bytes += int64(b)
		widths = append(widths, width(l))
	}
	return bytes, widths, nil
}

// padded extracts a line from chunk[lo:mid], pads it to
// the terminal width, and appends it to the line buffer.
func (v *viewport) padded(chunk []byte, lo, mid int) (int, int) {
	line := v.stripCarriageReturns(chunk[lo:mid])
	if !v.fixedHeight {
		pl := v.width - width(line)
		if pl < 0 {
			pl = 0
		}
		for range pl {
			line = append(line, ' ')
		}
	}
	line = append(line, '\n') // FIXME: windows is \r\n ?..
	v.lines = append(v.lines, line)
	mid++
	lo = mid
	return lo, mid
}

// appendToLinebuffer parses a raw chunk into display lines,
// handling escape sequences, line wrapping, and carriage returns.
func (v *viewport) appendToLinebuffer(chunk []byte) int {
	lo, mid, hi := 0, 0, len(chunk)
	var printed, addedLines int
	var escape bool
	for mid < hi {
		escape, printed = v.updateEscapeState(chunk[mid], escape, printed)
		var skipped bool
		lo, mid, skipped = v.skipCarriageReturn(chunk[mid], lo, mid)
		if skipped {
			continue
		}
		if printed > 0 && printed%v.width == 0 {
			lo, addedLines = v.addLine(chunk, lo, mid, addedLines)
		}
		if chunk[mid] == '\n' { // FIXME: windows is \r\n ?..
			lo, mid = v.padded(chunk, lo, mid)
			addedLines++
			printed = 0 // reset printed char count
			continue
		}
		if !escape {
			printed++
		}
		mid++
	}
	if lo < hi { // todo: check for escape seqs
		v.padded(chunk, lo, hi)
		addedLines++
	}
	return addedLines
}

// updateEscapeState tracks whether the current byte is
// inside an ANSI escape sequence and adjusts the print count.
func (*viewport) updateEscapeState(b byte, escape bool, printed int) (bool, int) {
	if escape && isEscapeEnd(b) {
		return false, printed
	}
	if isEscapeStart(b) {
		return true, printed - 1
	}
	return escape, printed
}

// skipCarriageReturn advances past a carriage return byte,
// adjusting the low and mid cursors accordingly.
func (*viewport) skipCarriageReturn(b byte, lo, mid int) (int, int, bool) {
	if b != '\r' {
		return lo, mid, false
	}
	if lo == mid {
		lo++
	}
	return lo, mid + 1, true
}

// addLine flushes the bytes between lo and mid as a
// complete line when a width-based wrap boundary is hit.
func (v *viewport) addLine(chunk []byte, lo, mid int, addedLines int) (int, int) {
	if lo < mid {
		raw := v.stripCarriageReturns(chunk[lo:mid])
		tmp := make([]byte, len(raw)+1)
		copy(tmp, raw)
		tmp[len(tmp)-1] = '\n'
		v.lines = append(v.lines, tmp)
		addedLines++
	}
	lo = mid
	return lo, addedLines
}

// stripCarriageReturns returns a copy of chunk
// with all carriage return bytes removed.
func (*viewport) stripCarriageReturns(chunk []byte) []byte {
	if bytes.IndexByte(chunk, '\r') < 0 {
		return append([]byte(nil), chunk...)
	}
	out := make([]byte, 0, len(chunk))
	for _, b := range chunk {
		if b != '\r' {
			out = append(out, b)
		}
	}
	return out
}

// loop is the viewport's single-goroutine event loop that
// serializes writes and writeTo requests to avoid data races.
func (v *viewport) loop() {
	for {
		// technically, we can cleanup the old lines here on a time interval,
		// maitaining "append" and "display" offsets
		select {
		case <-v.ctx.Done():
			return
		// from [viewport.Write]
		case req := <-v.inner:
			if req.width > 0 {
				v.width = req.width
			}
			v.lastLines = v.appendToLinebuffer(req.chunk)
			if !v.sendNotify(viewportChanged{lines: v.lastLines, done: req.done}) {
				return
			}
		// handled by [viewport.WriteTo]
		case w := <-v.writeTos:
			if !v.serveWriteTo(w) {
				return
			}
		}
	}
}

// sendNotify delivers ev to [chanIO.forwardTo] while still serving render
// requests, so a full notify queue cannot deadlock a redraw that waits on
// this viewport. It returns false once the context is done.
func (v *viewport) sendNotify(ev viewportChanged) bool {
	for {
		select {
		case <-v.ctx.Done():
			return false
		case v.notify <- ev:
			return true
		case w := <-v.writeTos:
			if !v.serveWriteTo(w) {
				return false
			}
		}
	}
}

// serveWriteTo renders into the requester's writer and replies.
// It returns false once the context is done.
func (v *viewport) serveWriteTo(w *writeTo) bool {
	bytes, widths, err := v.writeTo(w)
	select {
	case <-v.ctx.Done():
		return false
	case w.res <- writeToResponse{
		bytes:  bytes,
		lines:  len(widths),
		widths: widths,
		err:    err,
	}:
		return true
	}
}
