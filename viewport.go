// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"io"
)

type viewportChanged int

type writeToResponse struct {
	bytes int64
	lines int
	err   error
}

type writeTo struct {
	io.Writer
	res chan writeToResponse
}

type viewport struct {
	width, height int
	lines         [][]byte
	next          *viewport
	inner         chan []byte
	notify        chan viewportChanged
	writeTos      chan *writeTo
	ctx           context.Context
	fixedHeight   bool
	lastLines     int
}

func initViewport(ctx context.Context, notify chan viewportChanged, width, height int) *viewport {
	v := &viewport{
		ctx:      ctx,
		inner:    make(chan []byte),
		writeTos: make(chan *writeTo),
		notify:   notify,
		width:    width,
		height:   height,
	}
	go v.loop()
	return v
}

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
	select {
	case <-v.ctx.Done():
		return 0, io.EOF
	// [viewport.loop] will handle the write
	case v.inner <- chunk:
		return len(chunk), nil
	}
}

func (v *viewport) combinedHeight() int {
	var n int
	curr := v
	for curr != nil {
		n += curr.height
		curr = curr.next
	}
	return n
}

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
func (v *viewport) writeTo(w io.Writer) (int64, int, error) {
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
	var lines int
	var bytes int64
	for _, l := range v.lines {
		b, err := fmt.Fprintf(w, "\r%s", l)
		if err != nil {
			return bytes, lines, err
		}
		bytes += int64(b)
		lines++
	}
	return bytes, lines, nil
}

func (v *viewport) padded(chunk []byte, lo, mid int) (int, int) {
	pos, line := lo, []byte{}
	for pos < mid {
		line = append(line, chunk[pos])
		pos++
	}
	pl := v.width - width(chunk[lo:mid])
	if pl < 0 {
		pl = 0
	}
	for range pl {
		line = append(line, ' ')
	}
	line = append(line, '\n') // FIXME: windows is \r\n ?..
	v.lines = append(v.lines, line)
	mid++
	lo = mid
	return lo, mid
}

func (v *viewport) appendToLinebuffer(chunk []byte) int {
	lo, mid, hi := 0, 0, len(chunk)
	var printed, addedLines int
	var escape bool
	for mid < hi {
		if escape && isEscapeEnd(chunk[mid]) {
			escape = false
		} else if isEscapeStart(chunk[mid]) {
			escape = true
			printed--
		}
		// TODO: skip \r as well
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

func (v *viewport) addLine(chunk []byte, lo, mid int, addedLines int) (int, int) {
	if lo < mid {
		tmp := make([]byte, mid-lo+1)
		copy(tmp, chunk[lo:mid])
		tmp[len(tmp)-1] = '\n'
		v.lines = append(v.lines, tmp)
		addedLines++
	}
	lo = mid
	return lo, addedLines
}

func (v *viewport) loop() {
	for {
		// technically, we can cleanup the old lines here on a time interval,
		// maitaining "append" and "display" offsets
		select {
		case <-v.ctx.Done():
			return
		// from [viewport.Write]
		case chunk := <-v.inner:
			v.lastLines = v.appendToLinebuffer(chunk)
			select {
			case <-v.ctx.Done():
				return
			// notify is handled by [chanIO.forwardTo]
			case v.notify <- viewportChanged(v.lastLines):
			}
		// handled by [viewport.WriteTo]
		case w := <-v.writeTos:
			bytes, lines, err := v.writeTo(w)
			select {
			case <-v.ctx.Done():
				return
			// handled by [viewport.WriteTo]
			case w.res <- writeToResponse{bytes, lines, err}:
			}
		}
	}
}
