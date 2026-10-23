// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/errs"
	"io"
	"sync"

	"golang.org/x/term"
)

type Descriptor interface {
	Fd() uintptr
}

type IO struct {
	In            io.Reader
	Out           io.Writer
	Width, Height int
	// Restore gives the viewport back and leaves raw mode.
	Restore func() error
	// modeRestore only leaves raw mode, see [termIO.restoreMode].
	modeRestore func() error

	Cio *ChanIO
	Vp  *Viewport

	Pending      []byte
	pendingOrder uint64 // pending bytes retain their source-read order
	readOrder    uint64 // order of the most recent Read
	eventOrder   uint64 // order of the first bytes of the current event
	bm1, bm2     byte
	// SplitKeys decodes keys returned together by one read one at a time,
	// keeping the rest pending, instead of reporting them as a paste.
	SplitKeys bool
	// BracketedPaste opts into framed events; all reads retain paste boundaries.
	BracketedPaste bool

	Fd       int             // output file descriptor for resize refresh
	OnResize <-chan struct{} // fires on terminal resize (SIGWINCH)

	Input  *inputState     // input lifetime is independent of terminal acquisition
	reader <-chan struct{} // closed once the last [termIO.readEvents] goroutine is done with pending
}

var MakeRaw = term.MakeRaw
var Restore = term.Restore

// makeTermIO sets up raw terminal mode and creates
// a termIO for interactive widget rendering.
func makeTermIO(in io.Reader, out io.Writer) (*IO, error) {
	return acquireTermIO(in, out, rawRestore)
}

// MakeOutputIO acquires drawing resources without changing terminal mode,
// so restoring a progress bar or spinner cannot disable a concurrent prompt's raw mode.
func MakeOutputIO(in io.Reader, out io.Writer) (*IO, error) {
	return acquireTermIO(in, out, func(Descriptor) (func() error, error) {
		return func() error { return nil }, nil
	})
}

// acquireTermIO prepares direct or managed terminal I/O with the caller's mode policy
// and restores that mode if viewport acquisition fails.
func acquireTermIO(
	in io.Reader,
	out io.Writer,
	rawRestore func(Descriptor) (func() error, error),
) (*IO, error) {
	stderr, isOutFD := out.(Descriptor)
	cio, isOutChanIO := out.(*ChanIO)
	if !isOutFD && !isOutChanIO {
		return nil, fmt.Errorf("stderr: %w", errs.ErrNoTTY)
	}
	stdin, ok := in.(Descriptor)
	if !ok {
		return nil, fmt.Errorf("stdin: %w", errs.ErrNoTTY)
	}
	if cio != nil {
		restore, err := rawRestore(stdin)
		if err != nil {
			return nil, fmt.Errorf("raw: %w", err)
		}
		vp, err := cio.PushViewport()
		if err != nil {
			err = fmt.Errorf("viewport: %w", err)
			if rerr := restore(); rerr != nil {
				err = errors.Join(err, fmt.Errorf("restore: %w", rerr))
			}
			return nil, err
		}
		width, height := cio.size()
		return WithRestore(&IO{
			In:       in,
			Input:    SharedInputState(in),
			Out:      out,
			Width:    width,
			Height:   height, // first render will set the height
			Vp:       vp,
			Cio:      cio,
			OnResize: resizeNotify(),
		}, restore), nil
	}
	if Default.Env.Ci {
		return nil, fmt.Errorf("stderr: CI: %w", errs.ErrNoTTY)
	}
	fd := int(stderr.Fd())
	width, height, err := GetSize(fd)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errs.ErrNoTTY, err)
	}
	restore, err := rawRestore(stdin)
	if err != nil {
		return nil, fmt.Errorf("raw: %w", err)
	}
	return WithRestore(&IO{
		In:       in,
		Input:    SharedInputState(in),
		Out:      out,
		Width:    width,
		Height:   height,
		Fd:       fd,
		OnResize: resizeNotify(),
	}, restore), nil
}

// plainWidth is the geometry assumed for scripted input and output.
const plainWidth, plainHeight = 80, 24

// MakePromptIO acquires the terminal for prompts. Input that is not a file
// descriptor, e.g. a [strings.Reader] passed to WithInput, cannot be a terminal,
// so prompts then read it as scripted input and write frames to out as plain
// text without raw mode or a managed viewport. Real descriptors still require a terminal.
// Output that cannot host a terminal is never a prompt, whatever the input is.
func MakePromptIO(in io.Reader, out io.Writer) (*IO, error) {
	if !OutputIsTerminal(out) {
		return nil, fmt.Errorf("stderr: %w", errs.ErrNoTTY)
	}
	_, isInFD := in.(Descriptor)
	_, isOutFD := out.(Descriptor)
	_, isOutChanIO := out.(*ChanIO)
	if isInFD && (isOutFD || isOutChanIO) {
		return makeTermIO(in, out)
	}
	t := &IO{
		In:       in,
		Input:    SharedInputState(in),
		Out:      out,
		Width:    plainWidth,
		Height:   plainHeight,
		OnResize: nil, // never fires
	}
	t.modeRestore = func() error { return nil }
	t.Restore = t.modeRestore
	return t, nil
}

// WithRestore makes Restore release the viewport, then leave raw mode.
// Raw mode is left at most once: a repeated call must not restore the mode
// saved at acquisition over raw mode that a later prompt has entered.
func WithRestore(t *IO, restore func() error) *IO {
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

// RestoreMode leaves raw mode but keeps the viewport, for output-only
// widgets that keep drawing after acquisition.
func (t *IO) RestoreMode() error {
	if t.modeRestore == nil {
		return t.Restore()
	}
	return t.modeRestore()
}

// rawRestore puts stdin into raw mode and returns
// a function that restores the original state.
func rawRestore(stdin Descriptor) (func() error, error) {
	if !InputChecker(int(stdin.Fd())) {
		return func() error { return nil }, nil
	}
	oldState, err := MakeRaw(int(stdin.Fd()))
	if err != nil {
		return nil, err
	}
	return func() error {
		return Restore(int(stdin.Fd()), oldState)
	}, nil
}

// release gives the managed viewport back to the arbiter once the widget is
// done drawing. Its last frame stays in the scrollback. Safe to call more than once.
func (t *IO) release() {
	if t == nil || t.Cio == nil || t.Vp == nil {
		return
	}
	t.Cio.releaseViewport(t.Vp)
}

// Read preserves buffered arrival order and assigns an order to fresh source bytes.
func (t *IO) Read(p []byte) (n int, err error) {
	if len(t.Pending) > 0 {
		n = copy(p, t.Pending)
		t.readOrder = t.pendingOrder
		t.Pending = t.Pending[n:]
		return n, nil
	}
	n, err = t.In.Read(p)
	t.readOrder = NextInputOrder()
	return n, err
}

func (t *IO) Write(p []byte) (n int, err error) {
	if t.Vp != nil {
		// propagate current width so the viewport wraps correctly after resize
		return t.Vp.writeWithWidth(p, t.Width)
	}
	n, err = t.Out.Write(p)
	if err != nil || n < len(p) {
		restoreAutowrap(t.Out, p)
	}
	return n, err
}

// ReadKey reads a single byte and returns it as a rune,
// treating Ctrl-C/D as EOF.
func (t *IO) ReadKey() (rune, error) {
	buf := make([]byte, 1)
	_, err := t.Read(buf)
	if err != nil {
		return 0, err
	}
	// keep last two entered bytes
	t.bm1, t.bm2 = buf[0], t.bm1
	switch buf[0] {
	case KeyCtrlC, keyCtrlD:
		return 0, io.EOF
	default:
		return rune(buf[0]), nil
	}
}

// RefreshSize re-queries the terminal dimensions and updates
// Width/Height so the next render uses the current geometry.
func (t *IO) RefreshSize() {
	if t.Cio != nil {
		t.Cio.refreshSize()
		w, h := t.Cio.size()
		if w > 0 {
			t.Width = w
		}
		if h > 0 {
			t.Height = h
		}
		return
	}
	if t.Fd < 1 {
		return
	}
	w, h, err := GetSize(t.Fd)
	if err != nil {
		return
	}
	t.Width = w
	t.Height = h
}

// Clear erases the given number of terminal lines using
// ANSI cursor movement and line-Clear sequences.
//
//nolint:errcheck // TODO: improve error handling
func (t *IO) Clear(space int, buf io.Writer) error {
	if t.Vp != nil {
		if t.Vp.FixedHeight {
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
func (t *IO) clearFixedViewport(buf io.Writer) error {
	if buf != t {
		return nil
	}
	if t.Cio != nil {
		return t.Vp.WriteAndWait(nil)
	}
	_, err := t.Vp.Write(nil)
	return err
}
