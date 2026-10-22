// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"
	"unicode/utf8"
)

const (
	keyCtrlC = 0x03
	keyCtrlD = 0x04
	keyEnter = 0x0d
	// keyEscape is a lone Esc key press.
	keyEscape = 0x1b
	// keyIgnored stands for escape sequences that widgets do not handle,
	// such as Home, Delete, function keys, or Alt+key.
	keyIgnored = 0x00
)

// escapeTimeout bounds how long an incomplete escape sequence or UTF-8 rune
// waits for its next byte, so that a lone Esc is reported without waiting
// for another key.
var escapeTimeout = 50 * time.Millisecond

// isPasteText reports whether err carries buffered keys reported as a paste.
func isPasteText(err error) bool {
	target, ok := errors.AsType[*pasteTextError](err)
	return ok && target != nil
}

type pasteTextError struct {
	buf []byte
}

func (e *pasteTextError) Error() string {
	return fmt.Sprintf("bigger input (%d bytes)", len(e.buf))
}

// readRune reads and decodes a full rune, including
// multi-byte escape sequences for arrow keys.
func (t *termIO) readRune(ctx context.Context) (rune, int, error) {
	buf, n, err := t.readRuneBytes(ctx)
	if err != nil {
		return 0, n, err
	}
	return t.decodeRuneBytes(buf, n)
}

type keyEvent struct {
	key       rune
	err       error
	paste     []byte
	bracketed bool
	readOrder uint64
}

// readRuneBytes preserves escape-sequence boundaries before decoding an input event.
func (t *termIO) readRuneBytes(ctx context.Context) ([]byte, int, error) {
	buf := make([]byte, 16)
	n, err := t.Read(buf)
	t.eventOrder = t.readOrder
	if n == 0 {
		if err == nil {
			err = io.EOF
		}
		return nil, n, err
	}
	if n > 0 && buf[0] == keyEscape {
		buf, n, err = t.readEscape(ctx, buf, n, err)
		if err != nil {
			if n == 0 {
				return nil, n, err
			}
			return buf, n, err
		}
		return buf, n, nil
	}
	n = t.readRuneTail(ctx, buf, n, err)
	return buf, n, nil
}

// readRuneTail reads the remaining bytes of a multi-byte UTF-8 rune
// that was split across reads, one byte at a time.
func (t *termIO) readRuneTail(ctx context.Context, buf []byte, n int, readErr error) int {
	for readErr == nil && n > 0 && n < len(buf) && Text(buf[:n]).PartialRuneTail() > 0 {
		if !t.nextByteReady(ctx, escapeTimeout) {
			return n
		}
		m, err := t.Read(buf[n : n+1])
		n += m
		if err != nil || m == 0 {
			return n
		}
	}
	return n
}

// decodeRuneBytes interprets a byte buffer as a known escape
// rune, a paste event, or a single character.
func (t *termIO) decodeRuneBytes(buf []byte, n int) (rune, int, error) {
	if n == 0 {
		return keyIgnored, n, nil
	}
	r, consumed, ok := t.maybeKnownRune(buf[:n])
	if ok {
		t.pushPending(buf, consumed, n)
		return r, consumed, nil
	}
	if buf[0] == keyEscape {
		consumed, _ = t.escapeSequenceLen(buf[:n])
		t.pushPending(buf, consumed, n)
		if consumed == 1 {
			return keyEscape, consumed, nil
		}
		return keyIgnored, consumed, nil
	}
	if r, size := utf8.DecodeRune(buf[:n]); size == n || t.splitKeys {
		t.pushPending(buf, size, n)
		switch r {
		case keyCtrlC, keyCtrlD:
			return 0, size, io.EOF
		default:
			return r, size, nil
		}
	}
	n = t.holdPartialRune(buf, n)
	cp := make([]byte, n)
	copy(cp, buf[:n])
	return 0, n, &pasteTextError{buf: cp}
}

// holdPartialRune moves a trailing incomplete UTF-8 rune into pending,
// so that a paste that filled the buffer mid-rune completes on the next read.
// It returns the number of bytes left in buf.
func (t *termIO) holdPartialRune(buf []byte, n int) int {
	start := n - Text(buf[:n]).PartialRuneTail()
	if start == 0 || start == n {
		return n
	}
	t.pushPending(buf, start, n)
	return start
}

// readEscape reads additional bytes one at a time until the escape
// sequence is complete, no next byte arrives within [escapeTimeout],
// or the buffer fills. An OSC reply grows the buffer to [maxControlFrame].
func (t *termIO) readEscape(ctx context.Context, buf []byte, n int, readErr error) ([]byte, int, error) {
	wait := escapeTimeout
	for {
		if wait == escapeTimeout && t.isControlHeader(buf[:n]) {
			buf = slices.Grow(buf[:n], maxControlFrame-n)[:maxControlFrame]
			wait = controlFrameTimeout
		}
		if n >= len(buf) {
			return buf, n, readErr
		}
		if _, complete := t.escapeSequenceLen(buf[:n]); complete {
			return buf, n, readErr
		}
		if !t.nextByteReady(ctx, wait) {
			return buf, n, readErr
		}
		m, err := t.Read(buf[n : n+1])
		n += m
		if err != nil {
			return buf, n, err
		}
		if m == 0 {
			return buf, n, readErr
		}
	}
}

// nextByteReady waits up to wait for the next byte of an escape sequence
// or rune. Readers that cannot be polled always report ready and block in Read.
func (t *termIO) nextByteReady(ctx context.Context, wait time.Duration) bool {
	if len(t.pending) > 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	err := t.waitForReadableInput(ctx, t.in)
	return !errors.Is(err, context.DeadlineExceeded)
}

// escapeSequenceLen returns the length of the escape sequence at the start
// of buf and whether it is complete. A lone Esc has length 1, Alt+key has
// length 2, and SS3 and CSI sequences extend through their final byte. An OSC
// reply extends through its BEL or ST terminator, see [scanControlFrame].
func (t *termIO) escapeSequenceLen(buf []byte) (int, bool) {
	if len(buf) < 2 {
		return len(buf), false
	}
	switch b := buf[1]; {
	case b == 0x5b: // CSI
		p := Text(buf).CSISegment()
		return len(p.Text), !p.IsIncomplete
	case b == ']': // OSC
		if f := t.scanControlFrame(buf); !f.isInvalid {
			return f.size, f.isComplete
		}
		return 2, true // Alt+]
	case b == 0x4f: // SS3
		if len(buf) < 3 {
			return 2, false
		}
		if buf[2] >= 0x40 && buf[2] <= 0x7e {
			return 3, true
		}
		return 2, true // Alt+O
	case b >= 0x20 && b <= 0x7e: // Alt+key
		return 2, true
	default:
		// a control or non-ASCII byte is a separate key after a lone Esc
		return 1, true
	}
}

// maybeKnownRune checks whether buf starts with
// a recognized SS3 or CSI arrow-key escape sequence.
func (t *termIO) maybeKnownRune(buf []byte) (rune, int, bool) {
	if len(buf) < 3 || buf[0] != 0x1b {
		return 0, 0, false
	}
	switch buf[1] {
	case 0x4f: // SS3
		return t.decodeArrow(buf[2], 3)
	case 0x5b: // CSI
		return t.decodeCSIArrow(buf)
	default:
		return 0, 0, false
	}
}

// decodeArrow maps a final escape byte to an
// arrow-key rune (up/down/left/right).
func (*termIO) decodeArrow(b byte, consumed int) (rune, int, bool) {
	switch b {
	case 0x41:
		return '↑', consumed, true
	case 0x42:
		return '↓', consumed, true
	case 0x43:
		return '→', consumed, true
	case 0x44:
		return '←', consumed, true
	default:
		return 0, 0, false
	}
}

// decodeCSIArrow scans a CSI sequence for an arrow-key
// final byte, skipping numeric and semicolon parameters.
func (t *termIO) decodeCSIArrow(buf []byte) (rune, int, bool) {
	for i := 2; i < len(buf); i++ {
		r, consumed, ok := t.decodeArrow(buf[i], i+1)
		if ok {
			return r, consumed, true
		}
		if (buf[i] < '0' || buf[i] > '9') && buf[i] != ';' {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// pushPending retains unread bytes and their source arrival order for the next event.
func (t *termIO) pushPending(buf []byte, consumed, n int) {
	if consumed >= n {
		return
	}
	// Read drains one source buffer before fetching more, so all leftovers
	// share its arrival order. Prepend them to the remaining pending bytes.
	t.pending = append(buf[consumed:n:n], t.pending...)
	t.pendingOrder = t.readOrder
}

// see https://stackoverflow.com/a/37014283/277035
func isPrintable(r rune) bool {
	isSurrogate := r >= 0xd800 && r <= 0xdbff
	return r >= 32 && !isSurrogate
}
