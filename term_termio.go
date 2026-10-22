// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"io"
	"sync"

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
	if defaultIO.env.ci {
		return nil, fmt.Errorf("stderr: CI: %w", ErrNoTTY)
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
