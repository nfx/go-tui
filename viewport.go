// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"unicode/utf8"
)

type viewportChanged struct {
	lines   int
	done    chan struct{}
	release *viewport // set by [chanIO.releaseViewport]
}

type writeToResponse struct {
	bytes  int64
	lines  int
	widths []int
	err    error
}

type writeTo struct {
	io.Writer
	res    chan writeToResponse
	height int // if positive, the height budget to render within
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
	// partial holds a rune split across writes until the rest arrives.
	partial []byte

	cancel  context.CancelFunc // stops a managed viewport's loop
	release sync.Once          // guards [chanIO.releaseViewport]
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
		height := 0 // the first viewport keeps its own height
		if curr != v {
			height = budget
		}
		select {
		case <-curr.ctx.Done():
			return total, io.EOF
		// [viewport.loop] will handle the write and apply the height budget
		case curr.writeTos <- &writeTo{Writer: w, res: respond, height: height}:
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
				// simplified assumption: tail viewport cannot have fixed height
				curr = curr.next
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

// appendToLinebuffer parses a raw chunk into display lines, handling escape
// sequences, line wrapping, and carriage returns. It wraps by terminal columns
// with the same rune widths as [width] and never splits a rune or an escape sequence.
func (v *viewport) appendToLinebuffer(chunk []byte) int {
	lo, mid, hi := 0, 0, len(chunk)
	var printed, addedLines int
	for mid < hi {
		switch b := chunk[mid]; b {
		case '\r':
			lo, mid, _ = v.skipCarriageReturn(b, lo, mid)
		case '\n': // FIXME: windows is \r\n ?..
			lo, mid = v.padded(chunk, lo, mid)
			addedLines++
			printed = 0 // reset printed column count
		case keyEscape:
			// zero-width, stays on the current line
			mid += v.escapeLen(chunk[mid:])
		default:
			r, size := utf8.DecodeRune(chunk[mid:])
			w := runeWidth(r)
			if printed > 0 && v.width > 0 && printed+w > v.width {
				lo, addedLines = v.addLine(chunk, lo, mid, addedLines)
				printed = 0
			}
			printed += w
			mid += size
		}
	}
	if lo < hi {
		v.padded(chunk, lo, hi)
		addedLines++
	}
	return addedLines
}

// escapeLen returns the length of the escape sequence at the start of chunk,
// through its terminating letter, or the rest of chunk if it does not end.
func (*viewport) escapeLen(chunk []byte) int {
	for i := 1; i < len(chunk); i++ {
		if isEscapeEnd(chunk[i]) {
			return i + 1
		}
	}
	return len(chunk)
}

// joinPartialRune prepends a rune split by the previous write and holds back
// a rune split at the end of this one, so that lines never cut a character.
func (v *viewport) joinPartialRune(chunk []byte) []byte {
	if len(v.partial) > 0 {
		chunk = append(v.partial, chunk...)
		v.partial = nil
	}
	start := len(chunk) - 1
	for start > 0 && len(chunk)-start < utf8.UTFMax && !utf8.RuneStart(chunk[start]) {
		start--
	}
	if start < 0 || utf8.FullRune(chunk[start:]) {
		return chunk
	}
	v.partial = bytes.Clone(chunk[start:])
	return chunk[:start]
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
			if len(req.chunk) == 0 {
				v.partial = nil // an empty write clears the frame
			}
			// a write holding only part of a rune keeps the current frame
			if chunk := v.joinPartialRune(req.chunk); len(chunk) > 0 || len(req.chunk) == 0 {
				v.lastLines = v.appendToLinebuffer(chunk)
			}
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
	if w.height > 0 {
		v.height = w.height
	}
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
