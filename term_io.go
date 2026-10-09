// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

type descriptor interface {
	Fd() uintptr
}

type termIO struct {
	in            io.Reader
	out           io.Writer
	Width, Height int
	// Restore gives the viewport back and leaves raw mode.
	Restore func() error
	// modeRestore only leaves raw mode, see [termIO.restoreMode].
	modeRestore func() error

	cio *chanIO
	vp  *viewport

	pending  []byte
	bm1, bm2 byte
	// splitKeys decodes keys returned together by one read one at a time,
	// keeping the rest pending, instead of reporting them as a paste.
	splitKeys bool

	fd       int             // output file descriptor for resize refresh
	onResize <-chan struct{} // fires on terminal resize (SIGWINCH)

	input  *inputState     // input lifetime is independent of terminal acquisition
	reader <-chan struct{} // closed once the last [termIO.readEvents] goroutine is done with pending
}

var ErrNoTTY = errors.New("no tty")

var termMakeRaw = term.MakeRaw
var termRestore = term.Restore
var terminalInputChecker = func(fd int) bool {
	return term.IsTerminal(fd)
}

// makeTermIO sets up raw terminal mode and creates
// a termIO for interactive widget rendering.
func makeTermIO(in io.Reader, out io.Writer) (*termIO, error) {
	stderr, isOutFD := out.(descriptor)
	cio, isOutChanIO := out.(*chanIO)
	if !isOutFD && !isOutChanIO {
		return nil, fmt.Errorf("stderr: %w", ErrNoTTY)
	}
	stdin, ok := in.(descriptor)
	if !ok {
		return nil, fmt.Errorf("stdin: %w", ErrNoTTY)
	}
	if cio != nil {
		restore, err := rawRestore(stdin)
		if err != nil {
			return nil, fmt.Errorf("raw: %w", err)
		}
		vp, err := cio.pushViewport()
		if err != nil {
			err = fmt.Errorf("viewport: %w", err)
			if rerr := restore(); rerr != nil {
				err = errors.Join(err, fmt.Errorf("restore: %w", rerr))
			}
			return nil, err
		}
		width, height := cio.size()
		return withRestore(&termIO{
			in:       in,
			input:    sharedInputState(in),
			out:      out,
			Width:    width,
			Height:   height, // first render will set the height
			vp:       vp,
			cio:      cio,
			onResize: resizeNotify(),
		}, restore), nil
	}
	fd := int(stderr.Fd())
	width, height, err := termGetSize(fd)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoTTY, err)
	}
	restore, err := rawRestore(stdin)
	if err != nil {
		return nil, fmt.Errorf("raw: %w", err)
	}
	return withRestore(&termIO{
		in:       in,
		input:    sharedInputState(in),
		out:      out,
		Width:    width,
		Height:   height,
		fd:       fd,
		onResize: resizeNotify(),
	}, restore), nil
}

// withRestore makes Restore release the viewport, then leave raw mode.
// Raw mode is left at most once: a repeated call must not restore the mode
// saved at acquisition over raw mode that a later prompt has entered.
func withRestore(t *termIO, restore func() error) *termIO {
	var once sync.Once
	var err error
	t.modeRestore = func() error {
		once.Do(func() { err = restore() })
		return err
	}
	t.Restore = func() error {
		t.release()
		return t.modeRestore()
	}
	return t
}

// restoreMode leaves raw mode but keeps the viewport, for output-only
// widgets that keep drawing after acquisition.
func (t *termIO) restoreMode() error {
	if t.modeRestore == nil {
		return t.Restore()
	}
	return t.modeRestore()
}

// rawRestore puts stdin into raw mode and returns
// a function that restores the original state.
func rawRestore(stdin descriptor) (func() error, error) {
	if !terminalInputChecker(int(stdin.Fd())) {
		return func() error { return nil }, nil
	}
	oldState, err := termMakeRaw(int(stdin.Fd()))
	if err != nil {
		return nil, err
	}
	return func() error {
		return termRestore(int(stdin.Fd()), oldState)
	}, nil
}

// release gives the managed viewport back to the arbiter once the widget is
// done drawing. Its last frame stays in the scrollback. Safe to call more than once.
func (t *termIO) release() {
	if t == nil || t.cio == nil || t.vp == nil {
		return
	}
	t.cio.releaseViewport(t.vp)
}

// Read drains any buffered pending bytes before
// falling through to the underlying reader.
func (t *termIO) Read(p []byte) (n int, err error) {
	if len(t.pending) > 0 {
		n = copy(p, t.pending)
		t.pending = t.pending[n:]
		return n, nil
	}
	return t.in.Read(p)
}

func (t *termIO) Write(p []byte) (n int, err error) {
	if t.vp != nil {
		// propagate current width so the viewport wraps correctly after resize
		return t.vp.writeWithWidth(p, t.Width)
	}
	return t.out.Write(p)
}

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

type pasteTextError struct {
	buf []byte
}

func (e *pasteTextError) Error() string {
	return fmt.Sprintf("bigger input (%d bytes)", len(e.buf))
}

// ReadKey reads a single byte and returns it as a rune,
// treating Ctrl-C/D as EOF.
func (t *termIO) ReadKey() (rune, error) {
	buf := make([]byte, 1)
	_, err := t.Read(buf)
	if err != nil {
		return 0, err
	}
	// keep last two entered bytes
	t.bm1, t.bm2 = buf[0], t.bm1
	switch buf[0] {
	case keyCtrlC, keyCtrlD:
		return 0, io.EOF
	default:
		return rune(buf[0]), nil
	}
}

// readRune reads and decodes a full rune, including
// multi-byte escape sequences for arrow keys.
func (t *termIO) readRune() (rune, int, error) {
	buf, n, err := t.readRuneBytes()
	if err != nil {
		return 0, n, err
	}
	return t.decodeRuneBytes(buf, n)
}

type keyEvent struct {
	key   rune
	err   error
	paste []byte
}

// readEvents reads one event per permit; nil permits reads a single event.
// Cancellation retains an unconsumed read for the next prompt on this input.
func (t *termIO) readEvents(ctx context.Context, permits <-chan struct{}) <-chan keyEvent {
	// Finish publishing the previous key's pending bytes before starting a read.
	t.awaitReader()
	ch := make(chan keyEvent)
	done := make(chan struct{})
	t.reader = done
	go func() {
		defer close(done)
		defer close(ch)
		for {
			if permits != nil {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-permits:
					if !ok {
						return
					}
				}
			}
			if ctx.Err() != nil {
				return
			}
			// Buffered bytes never reach the fd again, so do not poll for them.
			if !t.hasInput() && len(t.pending) == 0 {
				if err := waitForReadableInput(ctx, t.in); err != nil {
					select {
					case <-ctx.Done():
					case ch <- keyEvent{err: err}:
					}
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
			read, consumed := t.startRead()
			select {
			case <-ctx.Done():
				return
			case <-read.done:
			}
			select {
			case <-ctx.Done():
				return
			case ch <- read.event:
				t.pending = read.pending
				consumed()
			}
			if permits == nil || (read.event.err != nil && read.event.paste == nil) {
				return
			}
		}
	}()
	return ch
}

// awaitReader waits until the last [termIO.readEvents] goroutine exits after its
// context is cancelled. It does not receive the key, so an unreceived key stays
// with the input for its next prompt.
func (t *termIO) awaitReader() {
	if t.reader != nil {
		<-t.reader
	}
}

// refreshSize re-queries the terminal dimensions and updates
// Width/Height so the next render uses the current geometry.
func (t *termIO) refreshSize() {
	if t.cio != nil {
		t.cio.refreshSize()
		w, h := t.cio.size()
		if w > 0 {
			t.Width = w
		}
		if h > 0 {
			t.Height = h
		}
		return
	}
	if t.fd < 1 {
		return
	}
	w, h, err := termGetSize(t.fd)
	if err != nil {
		return
	}
	t.Width = w
	t.Height = h
}

// readRuneBytes reads raw bytes and continues reading
// through escape sequences until a complete sequence is buffered.
func (t *termIO) readRuneBytes() ([]byte, int, error) {
	buf := make([]byte, 16)
	n, err := t.Read(buf)
	if n == 0 {
		if err == nil {
			err = io.EOF
		}
		return nil, n, err
	}
	if n > 0 && buf[0] == keyEscape {
		n, err = t.readEscape(buf, n, err)
		if err != nil {
			if n == 0 {
				return nil, n, err
			}
			return buf, n, err
		}
		return buf, n, nil
	}
	n = t.readRuneTail(buf, n, err)
	return buf, n, nil
}

// readRuneTail reads the remaining bytes of a multi-byte UTF-8 rune
// that was split across reads, one byte at a time.
func (t *termIO) readRuneTail(buf []byte, n int, readErr error) int {
	for readErr == nil && n > 0 && n < len(buf) && !t.endsWithFullRune(buf[:n]) {
		if !t.nextByteReady() {
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

// endsWithFullRune reports whether buf does not end in the middle of a UTF-8 rune.
func (*termIO) endsWithFullRune(buf []byte) bool {
	start := len(buf) - 1
	for start > 0 && len(buf)-start < utf8.UTFMax && !utf8.RuneStart(buf[start]) {
		start--
	}
	return utf8.FullRune(buf[start:])
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
	if t.endsWithFullRune(buf[:n]) {
		return n
	}
	start := n - 1
	for !utf8.RuneStart(buf[start]) {
		start--
	}
	if start == 0 {
		return n
	}
	t.pushPending(buf, start, n)
	return start
}

// readEscape reads additional bytes one at a time until the escape
// sequence is complete, no next byte arrives within [escapeTimeout],
// or the buffer fills.
func (t *termIO) readEscape(buf []byte, n int, readErr error) (int, error) {
	for n < len(buf) {
		if _, complete := t.escapeSequenceLen(buf[:n]); complete {
			return n, readErr
		}
		if !t.nextByteReady() {
			return n, readErr
		}
		m, err := t.Read(buf[n : n+1])
		n += m
		if err != nil {
			return n, err
		}
		if m == 0 {
			return n, readErr
		}
	}
	return n, readErr
}

// nextByteReady waits up to [escapeTimeout] for the next byte of an escape
// sequence or rune. Readers that cannot be polled always report ready and block in Read.
func (t *termIO) nextByteReady() bool {
	if len(t.pending) > 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), escapeTimeout)
	defer cancel()
	err := waitForReadableInput(ctx, t.in)
	return !errors.Is(err, context.DeadlineExceeded)
}

// escapeSequenceLen returns the length of the escape sequence at the start
// of buf and whether it is complete. A lone Esc has length 1, Alt+key has
// length 2, and SS3 and CSI sequences extend through their final byte.
func (*termIO) escapeSequenceLen(buf []byte) (int, bool) {
	if len(buf) < 2 {
		return len(buf), false
	}
	switch b := buf[1]; {
	case b == 0x5b: // CSI
		for i := 2; i < len(buf); i++ {
			c := buf[i]
			if c >= 0x20 && c <= 0x3f { // parameter and intermediate bytes
				continue
			}
			if c >= 0x40 && c <= 0x7e { // final byte
				return i + 1, true
			}
			// leave a byte that cannot belong to the sequence for the next read
			return i, true
		}
		return len(buf), false
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

func (t *termIO) pushPending(buf []byte, consumed, n int) {
	if consumed >= n {
		return
	}
	// unread bytes go before any bytes that are still pending
	t.pending = append(buf[consumed:n:n], t.pending...)
}

// clear erases the given number of terminal lines using
// ANSI cursor movement and line-clear sequences.
//
//nolint:errcheck // TODO: improve error handling
func (t *termIO) clear(space int, buf io.Writer) error {
	if t.vp != nil {
		if t.vp.fixedHeight {
			return t.clearFixedViewport(buf)
		}
		return nil // screen clearing is handled by [chanIO.forwardTo]
	}
	// use buffer to write to io only once
	// Move cursor up to the beginning of the dropdown
	fmt.Fprintf(buf, "\x1b[%dA", space)
	// Clear each line
	for i := range space {
		fmt.Fprint(buf, "\r")     // return to start of line
		fmt.Fprint(buf, "\x1b[K") // clear current line
		if i < space-1 {
			fmt.Fprint(buf, "\x1b[1B") // move cursor down if not last line
		}
	}
	// Move cursor back up to the beginning and to the start of the line
	if space > 1 {
		fmt.Fprintf(buf, "\x1b[%dA\r", space-1)
	}
	return nil
}

// clearFixedViewport clears a fixed-height overlay through its managed viewport.
func (t *termIO) clearFixedViewport(buf io.Writer) error {
	if buf != t {
		return nil
	}
	if t.cio != nil {
		return t.vp.writeAndWait(nil)
	}
	_, err := t.vp.Write(nil)
	return err
}

var terminalChecker = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func isTerminal() bool {
	return terminalChecker()
}

// see https://stackoverflow.com/a/37014283/277035
func isPrintable(r rune) bool {
	isSurrogate := r >= 0xd800 && r <= 0xdbff
	return r >= 32 && !isSurrogate
}

type inputRead struct {
	done    chan struct{}
	event   keyEvent
	pending []byte
}

// inputState retains only input decoding state across sequential prompt runs.
// The registry contains states with an outstanding read or buffered bytes.
type inputState struct {
	source  any
	read    *inputRead
	pending []byte
}

var inputStates = struct {
	sync.Mutex
	states map[any]*inputState
}{states: make(map[any]*inputState)}

func sharedInputState(in io.Reader) *inputState {
	// Files retain their existing per-run behavior. Non-comparable readers have
	// no reliable identity; never merge unrelated wrappers by file descriptor.
	if _, file := in.(*os.File); file || in == nil || !reflect.ValueOf(in).Comparable() {
		return &inputState{}
	}
	inputStates.Lock()
	defer inputStates.Unlock()
	if state := inputStates.states[in]; state != nil {
		return state
	}
	return &inputState{source: in}
}

func (t *termIO) inputState() *inputState {
	if t.input == nil {
		t.input = sharedInputState(t.in)
	}
	return t.input
}

func (t *termIO) hasInput() bool {
	state := t.inputState()
	inputStates.Lock()
	defer inputStates.Unlock()
	return state.read != nil || len(state.pending) != 0
}

// startRead adopts a previous prompt's read before starting another one.
func (t *termIO) startRead() (*inputRead, func()) {
	state := t.inputState()
	inputStates.Lock()
	defer inputStates.Unlock()
	if state.read != nil {
		read := state.read
		return read, func() { t.acceptRead(state, read) }
	}
	pending := state.pending
	if len(pending) == 0 {
		pending = t.pending
	}
	read := &inputRead{done: make(chan struct{})}
	state.read = read
	if state.source != nil {
		inputStates.states[state.source] = state
	}
	// The read owns its decoder even after its original prompt returns.
	decoder := &termIO{in: t.in, pending: pending, splitKeys: t.splitKeys}
	go func() {
		defer close(read.done)
		key, n, err := decoder.readRune()
		read.event = keyEvent{key: key, err: err}
		var more *pasteTextError
		if errors.As(err, &more) {
			read.event.paste = append([]byte(nil), more.buf[:n]...)
		}
		read.pending = decoder.pending
		// A failed read with no key or buffered bytes has nothing to hand off.
		// Release its source even when the cancelled prompt never accepts it.
		if n == 0 && err != nil && len(read.pending) == 0 {
			inputStates.Lock()
			if inputStates.states[state.source] == state {
				delete(inputStates.states, state.source)
			}
			inputStates.Unlock()
		}
	}()
	return read, func() { t.acceptRead(state, read) }
}

// acceptRead retains decoder bytes and releases idle registry entries.
func (t *termIO) acceptRead(state *inputState, read *inputRead) {
	inputStates.Lock()
	defer inputStates.Unlock()
	if state.read != read {
		return
	}
	state.read = nil
	state.pending = read.pending
	if state.source != nil && len(state.pending) == 0 && inputStates.states[state.source] == state {
		delete(inputStates.states, state.source)
	}
}
