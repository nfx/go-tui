// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"io"
	"slices"
	"time"
	"unicode/utf8"
)

const (
	KeyCtrlC = 0x03
	keyCtrlD = 0x04
	KeyEnter = 0x0d
	// KeyEscape is a lone Esc key press.
	KeyEscape = 0x1b
	// KeyIgnored stands for escape sequences that widgets do not handle,
	// such as Home, Delete, function keys, or Alt+key.
	KeyIgnored = 0x00
)

// escapeTimeout bounds how long an incomplete escape sequence or UTF-8 rune
// waits for its next byte, so that a lone Esc is reported without waiting
// for another key.
var escapeTimeout = 50 * time.Millisecond

// IsPasteText reports whether err carries buffered keys reported as a paste.
func IsPasteText(err error) bool {
	target, ok := errors.AsType[*PasteTextError](err)
	return ok && target != nil
}

type PasteTextError struct {
	Buf []byte
}

func (e *PasteTextError) Error() string {
	return fmt.Sprintf("bigger input (%d bytes)", len(e.Buf))
}

// ReadRuneCtx reads and decodes a full rune, including
// multi-byte escape sequences for arrow keys.
func (t *IO) ReadRuneCtx(ctx context.Context) (rune, int, error) {
	buf, n, err := t.readRuneBytes(ctx)
	if err != nil {
		return 0, n, err
	}
	return t.decodeRuneBytes(buf, n)
}

type KeyEvent struct {
	Key       rune
	Err       error
	Paste     []byte
	Bracketed bool
	ReadOrder uint64
}

// readRuneBytes preserves escape-sequence boundaries before decoding an input event.
func (t *IO) readRuneBytes(ctx context.Context) ([]byte, int, error) {
	buf := make([]byte, 16)
	n, err := t.Read(buf)
	t.eventOrder = t.readOrder
	if n == 0 {
		if err == nil {
			err = io.EOF
		}
		return nil, n, err
	}
	if n > 0 && buf[0] == KeyEscape {
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
func (t *IO) readRuneTail(ctx context.Context, buf []byte, n int, readErr error) int {
	for readErr == nil && n > 0 && n < len(buf) && ansi.Text(buf[:n]).PartialRuneTail() > 0 {
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
func (t *IO) decodeRuneBytes(buf []byte, n int) (rune, int, error) {
	if n == 0 {
		return KeyIgnored, n, nil
	}
	r, consumed, ok := t.maybeKnownRune(buf[:n])
	if ok {
		t.pushPending(buf, consumed, n)
		return r, consumed, nil
	}
	if buf[0] == KeyEscape {
		consumed, _ = t.escapeSequenceLen(buf[:n])
		t.pushPending(buf, consumed, n)
		if consumed == 1 {
			return KeyEscape, consumed, nil
		}
		return KeyIgnored, consumed, nil
	}
	if r, size := utf8.DecodeRune(buf[:n]); size == n || t.SplitKeys {
		t.pushPending(buf, size, n)
		switch r {
		case KeyCtrlC, keyCtrlD:
			return 0, size, io.EOF
		default:
			return r, size, nil
		}
	}
	n = t.holdPartialRune(buf, n)
	cp := make([]byte, n)
	copy(cp, buf[:n])
	return 0, n, &PasteTextError{Buf: cp}
}

// holdPartialRune moves a trailing incomplete UTF-8 rune into pending,
// so that a paste that filled the buffer mid-rune completes on the next read.
// It returns the number of bytes left in buf.
func (t *IO) holdPartialRune(buf []byte, n int) int {
	start := n - ansi.Text(buf[:n]).PartialRuneTail()
	if start == 0 || start == n {
		return n
	}
	t.pushPending(buf, start, n)
	return start
}

// readEscape reads additional bytes one at a time until the escape
// sequence is complete, no next byte arrives within [escapeTimeout],
// or the buffer fills. An OSC reply grows the buffer to [maxControlFrame].
func (t *IO) readEscape(ctx context.Context, buf []byte, n int, readErr error) ([]byte, int, error) {
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
func (t *IO) nextByteReady(ctx context.Context, wait time.Duration) bool {
	if len(t.Pending) > 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	err := t.waitForReadableInput(ctx, t.In)
	return !errors.Is(err, context.DeadlineExceeded)
}

// escapeSequenceLen returns the length of the escape sequence at the start
// of buf and whether it is complete. A lone Esc has length 1, Alt+key has
// length 2, and SS3 and CSI sequences extend through their final byte. An OSC
// reply extends through its BEL or ST terminator, see [scanControlFrame].
func (t *IO) escapeSequenceLen(buf []byte) (int, bool) {
	if len(buf) < 2 {
		return len(buf), false
	}
	switch b := buf[1]; {
	case b == 0x5b: // CSI
		p := ansi.Text(buf).CSISegment()
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
func (t *IO) maybeKnownRune(buf []byte) (rune, int, bool) {
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
func (*IO) decodeArrow(b byte, consumed int) (rune, int, bool) {
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
func (t *IO) decodeCSIArrow(buf []byte) (rune, int, bool) {
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
func (t *IO) pushPending(buf []byte, consumed, n int) {
	if consumed >= n {
		return
	}
	// Read drains one source buffer before fetching more, so all leftovers
	// share its arrival order. Prepend them to the remaining pending bytes.
	t.Pending = append(buf[consumed:n:n], t.Pending...)
	t.pendingOrder = t.readOrder
}

// see https://stackoverflow.com/a/37014283/277035
func isPrintable(r rune) bool {
	isSurrogate := r >= 0xd800 && r <= 0xdbff
	return r >= 32 && !isSurrogate
}
