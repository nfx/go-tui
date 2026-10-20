// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	pending      []byte
	pendingOrder uint64 // pending bytes retain their source-read order
	readOrder    uint64 // order of the most recent Read
	eventOrder   uint64 // order of the first bytes of the current event
	bm1, bm2     byte
	// splitKeys decodes keys returned together by one read one at a time,
	// keeping the rest pending, instead of reporting them as a paste.
	splitKeys bool
	// bracketedPaste opts into framed events; all reads retain paste boundaries.
	bracketedPaste bool

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
	return acquireTermIO(in, out, rawRestore)
}

// makeOutputIO acquires drawing resources without changing terminal mode,
// so restoring a progress bar or spinner cannot disable a concurrent prompt's raw mode.
func makeOutputIO(in io.Reader, out io.Writer) (*termIO, error) {
	return acquireTermIO(in, out, func(descriptor) (func() error, error) {
		return func() error { return nil }, nil
	})
}

// acquireTermIO prepares direct or managed terminal I/O with the caller's mode policy
// and restores that mode if viewport acquisition fails.
func acquireTermIO(
	in io.Reader,
	out io.Writer,
	rawRestore func(descriptor) (func() error, error),
) (*termIO, error) {
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

// plainWidth is the geometry assumed for scripted input and output.
const plainWidth, plainHeight = 80, 24

// makePromptIO acquires the terminal for prompts. Input that is not a file
// descriptor, e.g. a [strings.Reader] passed to WithInput, cannot be a terminal,
// so prompts then read it as scripted input and write frames to out as plain
// text without raw mode or a managed viewport. Real descriptors still require a terminal.
// Output that cannot host a terminal is never a prompt, whatever the input is.
func makePromptIO(in io.Reader, out io.Writer) (*termIO, error) {
	if !outputIsTerminal(out) {
		return nil, fmt.Errorf("stderr: %w", ErrNoTTY)
	}
	_, isInFD := in.(descriptor)
	_, isOutFD := out.(descriptor)
	_, isOutChanIO := out.(*chanIO)
	if isInFD && (isOutFD || isOutChanIO) {
		return makeTermIO(in, out)
	}
	t := &termIO{
		in:       in,
		input:    sharedInputState(in),
		out:      out,
		Width:    plainWidth,
		Height:   plainHeight,
		onResize: nil, // never fires
	}
	t.modeRestore = func() error { return nil }
	t.Restore = t.modeRestore
	return t, nil
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

// Read preserves buffered arrival order and assigns an order to fresh source bytes.
func (t *termIO) Read(p []byte) (n int, err error) {
	if len(t.pending) > 0 {
		n = copy(p, t.pending)
		t.readOrder = t.pendingOrder
		t.pending = t.pending[n:]
		return n, nil
	}
	n, err = t.in.Read(p)
	t.readOrder = nextInputOrder()
	return n, err
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
func (t *termIO) readRune(ctx context.Context) (rune, int, error) {
	buf, n, err := t.readRuneBytes(ctx)
	if err != nil {
		return 0, n, err
	}
	return t.decodeRuneBytes(buf, n)
}

// inputOrder orders reads, loading completion, and cancellation even when
// the system clock returns equal timestamps for successive events.
var inputOrder atomic.Uint64

func nextInputOrder() uint64 {
	return inputOrder.Add(1)
}

type keyEvent struct {
	key       rune
	err       error
	paste     []byte
	bracketed bool
	readOrder uint64
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
				if err := t.waitForReadableInput(ctx, t.in); err != nil {
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
			read, consumed := t.startRead(ctx)
			select {
			case <-ctx.Done():
				t.abandonPaste(read)
				return
			case <-read.done:
			}
			event := t.shapeEvent(read)
			select {
			case <-ctx.Done():
				t.abandonPaste(read)
				return
			case ch <- event:
				t.pending = read.pending
				t.pendingOrder = read.pendingOrder
				consumed()
			}
			if permits == nil || (read.event.err != nil && read.event.paste == nil) {
				return
			}
		}
	}()
	return ch
}

// shapeEvent adapts a completed read to the consumer's paste mode, dropping
// bracketed events whose opening bytes preceded cancellation.
func (t *termIO) shapeEvent(read *inputRead) keyEvent {
	inputStates.Lock()
	defer inputStates.Unlock()
	event := read.event
	if event.bracketed && event.readOrder < read.discardBefore {
		return keyEvent{key: keyIgnored}
	}
	if event.bracketed && !t.bracketedPaste {
		// Legacy consumers still receive their buffered-text event shape.
		event.err = &pasteTextError{buf: event.paste}
	}
	return event
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
	for readErr == nil && n > 0 && n < len(buf) && text(buf[:n]).partialRuneTail() > 0 {
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
	start := n - text(buf[:n]).partialRuneTail()
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
		p := text(buf).csiSegment()
		return len(p.text), !p.isIncomplete
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

// see https://stackoverflow.com/a/37014283/277035
func isPrintable(r rune) bool {
	isSurrogate := r >= 0xd800 && r <= 0xdbff
	return r >= 32 && !isSurrogate
}

type inputRead struct {
	done          chan struct{}
	event         keyEvent
	discardBefore uint64
	pending       []byte
	pendingOrder  uint64
}

// inputState retains only input decoding state across sequential prompt runs.
// The registry contains states with an outstanding read or buffered bytes.
type inputState struct {
	source       any
	read         *inputRead
	pending      []byte
	pendingOrder uint64
	// replay holds decoded events that precede the in-flight read.
	replay []keyEvent
	// queries await terminal replies, guarded by inputStates.
	queries []*controlQuery
}

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

var inputStates = struct {
	sync.Mutex
	states map[any]*inputState
}{states: make(map[any]*inputState)}

// sharedInputState shares retained reads only through a comparable reader identity.
func sharedInputState(in io.Reader) *inputState {
	// Reader identity is never inferred from a descriptor number.
	if in == nil || !reflect.ValueOf(in).Comparable() {
		return &inputState{}
	}
	inputStates.Lock()
	defer inputStates.Unlock()
	if state := inputStates.states[in]; state != nil {
		return state
	}
	// Idle files remain local; outstanding reads retain this exact file object.
	if _, file := in.(*os.File); file {
		return &inputState{}
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
	return state.read != nil || len(state.pending) != 0 || len(state.replay) != 0
}

// startRead replays consumed input or adopts an outstanding read before reading anew.
func (t *termIO) startRead(ctx context.Context) (*inputRead, func()) {
	state := t.inputState()
	inputStates.Lock()
	defer inputStates.Unlock()
	// Retain file identity to prevent competing reads after raw mode ends.
	if state.source == nil {
		if _, file := t.in.(*os.File); file {
			state.source = t.in
		}
	}
	if len(state.replay) != 0 {
		return t.startReplay(state)
	}
	if state.read != nil {
		read := state.read
		return read, func() { t.acceptRead(state, read) }
	}
	pending, pendingOrder := state.pending, state.pendingOrder
	if len(pending) == 0 {
		pending, pendingOrder = t.pending, t.pendingOrder
	}
	read := &inputRead{done: make(chan struct{})}
	state.read = read
	if state.source != nil {
		inputStates.states[state.source] = state
	}
	// The read owns its decoder even after its original prompt returns.
	decoder := &termIO{
		in: t.in, input: state, pending: pending, pendingOrder: pendingOrder, splitKeys: t.splitKeys,
	}
	readCtx := context.WithoutCancel(ctx)
	go func() {
		defer close(read.done)
		event, n := decoder.readEvent(readCtx)
		read.event = event
		err := event.err
		read.pending = decoder.pending
		read.pendingOrder = decoder.pendingOrder
		// Release empty failed reads even if the cancelled prompt never accepts them.
		if n == 0 && err != nil && len(read.pending) == 0 {
			inputStates.Lock()
			if len(state.replay) == 0 && inputStates.states[state.source] == state {
				delete(inputStates.states, state.source)
			}
			inputStates.Unlock()
		}
	}()
	return read, func() { t.acceptRead(state, read) }
}

// startReplay hands over the oldest retained event, consuming it only on
// acceptance. Its pending bytes are the caller's own, so delivery keeps them.
// Requires inputStates held.
func (t *termIO) startReplay(state *inputState) (*inputRead, func()) {
	read := &inputRead{
		done:         make(chan struct{}),
		event:        state.replay[0],
		pending:      t.pending,
		pendingOrder: t.pendingOrder,
	}
	close(read.done)
	return read, func() {
		inputStates.Lock()
		defer inputStates.Unlock()
		state.replay = state.replay[1:]
		if state.source != nil && state.read == nil && len(state.pending) == 0 && len(state.replay) == 0 &&
			inputStates.states[state.source] == state {
			delete(inputStates.states, state.source)
		}
	}
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
	state.pendingOrder = read.pendingOrder
	if state.source != nil && len(state.pending) == 0 && len(state.replay) == 0 &&
		inputStates.states[state.source] == state {
		delete(inputStates.states, state.source)
	}
}

// readEvent keeps paste framing with the retained read so partial payloads cannot become keys.
func (t *termIO) readEvent(ctx context.Context) (event keyEvent, size int) {
	defer func() { event.readOrder = t.eventOrder }()
	buf, n, err := t.readRuneBytes(ctx)
	for err == nil {
		// Terminal replies are out-of-band: route them and keep waiting for a
		// key, so they neither become input nor end this read.
		f := t.scanControlFrame(buf[:n])
		if !f.isComplete {
			break
		}
		t.pushPending(buf, f.size, n)
		t.inputState().routeControl(f.reply)
		buf, n, err = t.readRuneBytes(ctx)
	}
	if err != nil {
		return keyEvent{err: err}, n
	}
	if bytes.HasPrefix(buf[:n], []byte("\x1b[200~")) {
		t.pushPending(buf, 6, n)
		payload, err := t.readBracketedPayload()
		if err != nil {
			return keyEvent{err: err}, 0
		}
		return keyEvent{paste: payload, bracketed: true}, len(payload) + 12
	}
	key, n, err := t.decodeRuneBytes(buf, n)
	event = keyEvent{key: key, err: err}
	var more *pasteTextError
	if errors.As(err, &more) {
		event.paste = append([]byte(nil), more.buf[:n]...)
	}
	return event, n
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

// replayKeys retains a coalesced event's arrival order and post-Enter bytes.
func (t *termIO) replayKeys(event keyEvent) {
	t.awaitReader()
	state := t.inputState()
	inputStates.Lock()
	defer inputStates.Unlock()
	state.pending = append(append([]byte(nil), event.paste...), state.pending...)
	state.pendingOrder = event.readOrder
	t.pending, t.pendingOrder = state.pending, state.pendingOrder
	if state.source != nil {
		inputStates.states[state.source] = state
	}
}

// outputIsTerminal reports whether prompts can draw on out. Without a terminal
// every prompt answers from its default or fails with [ErrNoTTY]. Tests that
// drive the interactive path with in-memory writers replace it.
var outputIsTerminal = hostsTerminal

// hostsTerminal accepts a managed terminal or a descriptor of a real one. A
// writer without terminal capabilities, such as [bytes.Buffer], a pipe or a
// redirected file, is not a terminal, however the input is wrapped.
func hostsTerminal(out io.Writer) bool {
	switch w := out.(type) {
	case *chanIO, *viewport, *Tui:
		return true
	case descriptor:
		_, _, err := termGetSize(int(w.Fd()))
		return err == nil
	}
	return false
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

// backgroundProbeSupported excludes Windows consoles without VT input enabled.
var backgroundProbeSupported = runtime.GOOS != "windows"

// canProbe checks for terminal input and a control output path.
func (t *termIO) canProbe() bool {
	in, ok := t.in.(descriptor)
	if !ok || !backgroundProbeSupported || !terminalInputChecker(int(in.Fd())) {
		return false
	}
	return t.cio != nil || t.fd > 0
}

// detectBackground uses the existing input owner for OSC 11, then falls back
// to COLORFGBG or dark. [tio.background] handles caching.
func (t *termIO) detectBackground(
	ctx context.Context,
	lookup func(string) (string, bool),
) background {
	if t.canProbe() {
		if b, ok := t.probeBackground(ctx); ok {
			return b
		}
	}
	return t.backgroundFromEnv(lookup)
}

// backgroundProbeTimeout bounds each OSC 11 query.
const backgroundProbeTimeout = 200 * time.Millisecond

// probeBackground queries OSC 11 without consuming keys; invalid replies fail.
func (t *termIO) probeBackground(ctx context.Context) (background, bool) {
	ctx, cancel := context.WithTimeout(ctx, backgroundProbeTimeout)
	// Register before reading to catch fast replies; drain and replay intervening keys.
	q := t.expectControl(11)
	defer q.cancel()
	permits := make(chan struct{})
	keys := t.readEvents(ctx, permits)
	var typed []keyEvent
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range keys {
			// Readiness failures carry no input; every read-backed event, including
			// paste and Ctrl-C/D errors, was acknowledged and must be replayed.
			if ev.readOrder == 0 {
				continue
			}
			if ev.bracketed {
				ev.err = nil // undo [termIO.shapeEvent] so replay reshapes for its consumer
			}
			typed = append(typed, ev)
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case permits <- struct{}{}:
			}
		}
	}()
	defer func() {
		cancel()
		t.awaitReader()
		<-drained
		t.restoreKeys(typed)
	}()
	err := t.writeControl(ctx, []byte("\x1b]11;?\x07"))
	if err != nil {
		return background{}, false
	}
	select {
	case <-ctx.Done():
		return background{}, false
	case reply := <-q.reply:
		rgb, ok := t.parseOSCColor(reply.data)
		if !ok {
			return background{}, false
		}
		return background{
			rgb:      rgb,
			light:    rgb.isLight(),
			measured: true,
		}, true
	}
}

// restoreKeys puts events read while probing back in front of the unread input,
// keeping their boundaries and arrival orders.
func (t *termIO) restoreKeys(events []keyEvent) {
	if len(events) == 0 {
		return
	}
	state := t.inputState()
	inputStates.Lock()
	defer inputStates.Unlock()
	state.replay = append(events, state.replay...)
	if state.source != nil {
		inputStates.states[state.source] = state
	}
}

// background describes terminal appearance; zero means the dark fallback.
type background struct {
	// rgb is valid only when measured; otherwise use the variant's reference.
	rgb sRGB
	// light selects the variant, using an estimate when unmeasured.
	light bool
	// measured distinguishes terminal RGB from palette estimates.
	measured bool
}

// parseOSCColor scales xterm rgb:R/G/B components of one to four hex digits to bytes.
func (t *termIO) parseOSCColor(data string) (rgb sRGB, ok bool) {
	spec, found := strings.CutPrefix(data, "rgb:")
	if !found {
		return rgb, false
	}
	parts := strings.Split(spec, "/")
	if len(parts) != 3 {
		return rgb, false
	}
	for i, p := range parts {
		if len(p) < 1 || len(p) > 4 {
			return rgb, false
		}
		v, err := strconv.ParseUint(p, 16, 16)
		if err != nil {
			return rgb, false
		}
		maxV := uint64(1)<<(4*len(p)) - 1
		rgb[i] = uint8((v*255 + maxV/2) / maxV)
	}
	return rgb, true
}

// parseColorFGBG estimates the background from fg;bg or fg;xpm;bg using the ANSI palette.
func (t *termIO) parseColorFGBG(v string) (rgb sRGB, ok bool) {
	fields := strings.Split(v, ";")
	if len(fields) < 2 || len(fields) > 3 {
		return rgb, false
	}
	i, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil || i < 0 || i > 255 {
		return rgb, false
	}
	return ansiPalette()[i], true
}

// backgroundFromEnv classifies COLORFGBG, or falls back to dark.
func (t *termIO) backgroundFromEnv(lookup func(string) (string, bool)) background {
	if lookup == nil {
		return background{}
	}
	v, _ := lookup("COLORFGBG")
	rgb, ok := t.parseColorFGBG(v)
	if !ok {
		return background{}
	}
	return background{light: rgb.isLight()}
}
