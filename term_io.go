// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

type descriptor interface {
	Fd() uintptr
}

type termIO struct {
	in            io.Reader
	out           io.Writer
	Width, Height int
	Restore       func() error

	cio *chanIO
	vp  *viewport

	bm1, bm2 byte
}

var ErrNoTTY = errors.New("no tty")

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
		vp, err := cio.pushViewport()
		if err != nil {
			return nil, fmt.Errorf("viewport: %w", err)
		}

		return &termIO{
			in:     in,
			out:    out,
			Width:  cio.width,
			Height: vp.height, // first render will set the height
			vp:     vp,
			cio:    cio,
			Restore: func() error {
				return nil
			},
		}, nil
	}
	width, height, err := term.GetSize(int(stderr.Fd()))
	if err != nil {
		return nil, fmt.Errorf("size: %w", err)
	}
	oldState, err := term.MakeRaw(int(stdin.Fd()))
	if err != nil {
		return nil, fmt.Errorf("raw: %w", err)
	}

	return &termIO{
		in:     in,
		out:    out,
		Width:  width,
		Height: height,
		Restore: func() error {
			return term.Restore(int(stdin.Fd()), oldState)
		},
	}, nil
}

func (t *termIO) Read(p []byte) (n int, err error) {
	return t.in.Read(p)
}

func (t *termIO) Write(p []byte) (n int, err error) {
	if t.vp != nil {
		return t.vp.Write(p)
	}

	return t.out.Write(p)
}

const (
	keyCtrlC = 0x03
	keyCtrlD = 0x04
	keyEnter = 0x0d
)

type pasteTextError struct {
	buf []byte
}

func (e *pasteTextError) Error() string {
	return fmt.Sprintf("bigger input (%d bytes)", len(e.buf))
}

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

func (t *termIO) ReadRune() (rune, int, error) {
	buf := make([]byte, 4)
	n, err := t.Read(buf) // todo: fixme
	if errors.Is(err, io.EOF) {
		return keyCtrlD, n, io.EOF
	}
	r, ok := t.maybeKnownRune(buf[:n])
	if ok {
		return r, n, nil
	}
	if n > 1 {
		return 0, n, &pasteTextError{buf}
	}
	switch buf[0] {
	case keyCtrlC, keyCtrlD:
		return 0, n, io.EOF
	default:
		return rune(buf[0]), n, nil
	}
}

func (t *termIO) maybeKnownRune(buf []byte) (rune, bool) {
	if len(buf) < 3 {
		return 0, false
	}
	if buf[0] != 0x1b && buf[1] != 0x5b {
		return 0, false
	}
	switch buf[2] {
	case 0x41: // Up arrow.
		return '↑', true
	case 0x42: // Down arrow.
		return '↓', true
	case 0x43: // Right arrow.
		return '→', true
	case 0x44: // Left arrow.
		return '←', true
	default:
		return 0, false
	}
}

//nolint:errcheck // TODO: improve error handling
func (t *termIO) clear(space int, buf io.Writer) error {
	if t.vp != nil {
		if t.vp.fixedHeight {
			return t.vp.WriteByte('\r')
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

func isTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// see https://stackoverflow.com/a/37014283/277035
func isPrintable(r rune) bool {
	isSurrogate := r >= 0xd800 && r <= 0xdbff

	return r >= 32 && !isSurrogate
}
