// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"time"
)

// routeControl hands a reply to the oldest query waiting for its code.
// Unclaimed replies are dropped: they are answers to queries that gave up.
func (s *inputState) routeControl(reply controlReply) {
	inputStates.Lock()
	defer inputStates.Unlock()
	i := slices.IndexFunc(s.queries, func(q *controlQuery) bool { return q.code == reply.code })
	if i < 0 {
		return
	}
	q := s.queries[i]
	s.queries = slices.Delete(s.queries, i, i+1)
	q.reply <- reply // buffered, and the query leaves the list on its first reply
}

// maxControlFrame bounds an OSC reply, introducer and terminator included.
// Replies to color queries are under 40 bytes, so anything longer is not one.
const maxControlFrame = 256

// controlFrameTimeout bounds the wait for the next byte of an OSC reply whose
// header is already recognized. Unlike a lone Esc or Alt+key, such a prefix is
// never typed, and a slow link may split a reply across several reads.
var controlFrameTimeout = 500 * time.Millisecond

// controlReply is a terminal's answer to a query, an OSC frame without
// its introducer and terminator, e.g. {11, "rgb:0000/0000/0000"}.
type controlReply struct {
	code int
	data string
}

// controlFrame is what [scanControlFrame] found at the start of input.
type controlFrame struct {
	size int // bytes scanned so far, or the frame length once complete; 0 if invalid
	// isInvalid is not an OSC reply: typed Alt+], garbage, or oversized.
	isInvalid bool
	// isIncomplete is a valid prefix that needs more bytes.
	isIncomplete bool
	// isComplete ends with BEL or ST.
	isComplete bool
	reply      controlReply // set only when complete
}

// scanControlFrame classifies the OSC reply at the start of buf. A reply has the
// form ESC ] Ps ; Pt terminated by BEL or ST (ESC \). Input that does not start
// with ESC ] is never a reply.
func (t *termIO) scanControlFrame(buf []byte) controlFrame {
	const header = 2 // ESC ]
	if len(buf) < header || buf[0] != keyEscape || buf[1] != ']' {
		return controlFrame{isInvalid: true}
	}
	i, code := header, 0
	for ; i < len(buf) && buf[i] >= '0' && buf[i] <= '9'; i++ {
		if i-header == 3 { // at most three digits
			return controlFrame{isInvalid: true}
		}
		code = code*10 + int(buf[i]-'0')
	}
	if i == len(buf) {
		return controlFrame{size: len(buf), isIncomplete: true}
	}
	if i == header || buf[i] != ';' {
		return controlFrame{isInvalid: true}
	}
	start := i + 1
	for i = start; i < len(buf); i++ {
		if i >= maxControlFrame {
			return controlFrame{isInvalid: true}
		}
		switch b := buf[i]; {
		case b == 0x07: // BEL
			return controlFrame{
				size:       i + 1,
				isComplete: true,
				reply:      controlReply{code: code, data: string(buf[start:i])},
			}
		case b == keyEscape:
			if i+1 == len(buf) {
				return controlFrame{size: len(buf), isIncomplete: true}
			}
			if buf[i+1] != '\\' { // ST
				return controlFrame{isInvalid: true}
			}
			return controlFrame{
				size:       i + 2,
				isComplete: true,
				reply:      controlReply{code: code, data: string(buf[start:i])},
			}
		case b < 0x20 || b == 0x7f:
			return controlFrame{isInvalid: true}
		}
	}
	return controlFrame{size: len(buf), isIncomplete: true}
}

// isControlHeader reports whether buf is an OSC reply prefix that cannot be a
// key press: it has the numeric code and the separator.
func (t *termIO) isControlHeader(buf []byte) bool {
	return !t.scanControlFrame(buf).isInvalid && bytes.IndexByte(buf, ';') > 0
}

// readBracketedPayload consumes a complete frame and preserves bytes after its closing marker.
func (t *termIO) readBracketedPayload() ([]byte, error) {
	end := []byte("\x1b[201~")
	var payload []byte
	var next [1024]byte
	for {
		start := max(0, len(payload)-len(end)+1)
		n, err := t.Read(next[:])
		payload = append(payload, next[:n]...)
		if offset := bytes.Index(payload[start:], end); offset >= 0 {
			offset += start
			t.pushPending(payload, offset+len(end), len(payload))
			return payload[:offset], nil
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.EOF
		}
	}
}

// abandonPaste discards a frame whose opening bytes arrived before cancellation.
// A still-blocked read remains available for fresh paste in the next prompt.
func (t *termIO) abandonPaste(read *inputRead) {
	inputStates.Lock()
	defer inputStates.Unlock()
	read.discardBefore = nextInputOrder()
}

// controlQuery waits for the terminal's reply to one control request.
type controlQuery struct {
	state *inputState
	code  int
	reply chan controlReply // receives the first reply with the awaited code
}

// cancel stops waiting. A reply that arrives later is still read from the
// terminal and discarded, so it can never be mistaken for typed text.
func (q *controlQuery) cancel() {
	inputStates.Lock()
	defer inputStates.Unlock()
	q.state.queries = slices.DeleteFunc(q.state.queries, func(o *controlQuery) bool { return o == q })
}

// expectControl registers interest in the reply with the given OSC code before
// the request is written, so a fast reply cannot be missed. Replies are routed
// from the input reader that owns pending bytes, see [inputState.routeControl].
func (t *termIO) expectControl(code int) *controlQuery {
	state := t.inputState()
	q := &controlQuery{state: state, code: code, reply: make(chan controlReply, 1)}
	inputStates.Lock()
	defer inputStates.Unlock()
	state.queries = append(state.queries, q)
	return q
}

// writeControl sends a terminal request such as an OSC query. It is not frame
// content: it bypasses viewports, so a redraw cannot replay it, and the
// append-only line buffer, so it is not held back until a newline.
func (t *termIO) writeControl(ctx context.Context, p []byte) error {
	switch {
	case t.cio != nil:
		return t.cio.writeControl(ctx, p)
	case t.fd > 0:
		_, err := t.out.Write(p)
		return err
	}
	return fmt.Errorf("control: %w", ErrNoTTY)
}
