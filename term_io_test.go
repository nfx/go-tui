// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/assert"
	"golang.org/x/term"
)

type mockDescriptor struct {
	io.Reader
	io.Writer
	fd uintptr
}

func (m *mockDescriptor) Fd() uintptr {
	return m.fd
}

type mockReader struct {
	io.Reader
}

type mockWriter struct {
	io.Writer
}

func TestMakeTermIO_NoChanIO_NoDescriptor(t *testing.T) {
	in := &mockReader{}
	out := &mockWriter{}

	_, err := makeTermIO(in, out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stderr")
	assert.True(t, errors.Is(err, ErrNoTTY))
}

func TestMakeTermIO_NoDescriptorInput(t *testing.T) {
	in := &mockReader{}
	out := &mockDescriptor{fd: 1}

	_, err := makeTermIO(in, out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stdin")
	assert.True(t, errors.Is(err, ErrNoTTY))
}

func TestMakeTermIO_WithChanIO(t *testing.T) {
	cio, _ := chainIOforTest(t, 40, 5)
	in := &mockDescriptor{
		Reader: bytes.NewBuffer(nil),
		Writer: bytes.NewBuffer(nil),
		fd:     0,
	}

	termio, err := makeTermIO(in, cio)
	assert.NoError(t, err)
	if termio == nil {
		t.Fatal("expected termIO")
	}
	if !reflect.DeepEqual(termio.in, in) {
		t.Fatalf("unexpected stdin")
	}
	if termio.Width != cio.width {
		t.Fatalf("unexpected width")
	}
	if termio.Restore == nil {
		t.Fatalf("restore not set")
	}
	err = termio.Restore()
	assert.NoError(t, err)
}

func TestMakeTermIO_WithChanIO_RawTTYInput(t *testing.T) {
	cio, _ := chainIOforTest(t, 40, 5)
	origCheck := terminalInputChecker
	origRaw := termMakeRaw
	origRestore := termRestore
	terminalInputChecker = func(int) bool { return true }
	termMakeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	termRestore = func(int, *term.State) error { return nil }
	t.Cleanup(func() {
		terminalInputChecker = origCheck
		termMakeRaw = origRaw
		termRestore = origRestore
	})
	in := &mockDescriptor{
		Reader: bytes.NewBuffer(nil),
		Writer: bytes.NewBuffer(nil),
		fd:     0,
	}

	termio, err := makeTermIO(in, cio)
	assert.NoError(t, err)
	assert.NotNil(t, termio)
	assert.NoError(t, termio.Restore())
}

func TestMakeTermIO_WithChanIO_RawError(t *testing.T) {
	cio, _ := chainIOforTest(t, 40, 5)
	origCheck := terminalInputChecker
	origRaw := termMakeRaw
	terminalInputChecker = func(int) bool { return true }
	termMakeRaw = func(int) (*term.State, error) { return nil, io.EOF }
	t.Cleanup(func() {
		terminalInputChecker = origCheck
		termMakeRaw = origRaw
	})
	in := &mockDescriptor{
		Reader: bytes.NewBuffer(nil),
		Writer: bytes.NewBuffer(nil),
		fd:     0,
	}

	_, err := makeTermIO(in, cio)
	assert.Error(t, err)
}

func TestMakeTermIO_WithDescriptor(t *testing.T) {
	if !isTerminal() {
		t.Skip("not a terminal")
	}

	in := os.Stdin
	out := os.Stderr

	termIO, err := makeTermIO(in, out)
	if err != nil {
		t.Skipf("failed to create termIO: %v", err)
	}

	assert.NotNil(t, termIO)
	assert.Equal(t, in, termIO.in)
	assert.Equal(t, out, termIO.out)
	assert.True(t, termIO.Width > 0)
	assert.True(t, termIO.Height > 0)
	assert.NotNil(t, termIO.Restore)

	// Clean up
	err = termIO.Restore()
	assert.NoError(t, err)
}

func TestMakeTermIO_WithDescriptorStubbed(t *testing.T) {
	origGet := termGetSize
	origRaw := termMakeRaw
	origRestore := termRestore
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	termMakeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	termRestore = func(int, *term.State) error { return nil }
	t.Cleanup(func() {
		termGetSize = origGet
		termMakeRaw = origRaw
		termRestore = origRestore
	})
	in := &mockDescriptor{
		Reader: bytes.NewBuffer(nil),
		Writer: bytes.NewBuffer(nil),
		fd:     0,
	}
	out := &mockDescriptor{
		Reader: bytes.NewBuffer(nil),
		Writer: bytes.NewBuffer(nil),
		fd:     1,
	}
	termio, err := makeTermIO(in, out)
	assert.NoError(t, err)
	if termio.Width != 80 || termio.Height != 24 {
		t.Fatalf("unexpected size %dx%d", termio.Width, termio.Height)
	}
	if err := termio.Restore(); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
}

func TestMakeTermIO_RawError(t *testing.T) {
	origGet := termGetSize
	origRaw := termMakeRaw
	origCheck := terminalInputChecker
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	termMakeRaw = func(int) (*term.State, error) { return nil, io.EOF }
	terminalInputChecker = func(int) bool { return true }
	t.Cleanup(func() {
		termGetSize = origGet
		termMakeRaw = origRaw
		terminalInputChecker = origCheck
	})
	in := &mockDescriptor{
		Reader: bytes.NewBuffer(nil),
		Writer: bytes.NewBuffer(nil),
		fd:     0,
	}
	out := &mockDescriptor{
		Reader: bytes.NewBuffer(nil),
		Writer: bytes.NewBuffer(nil),
		fd:     1,
	}
	_, err := makeTermIO(in, out)
	assert.Error(t, err)
}

func TestTermIO_Read(t *testing.T) {
	buf := bytes.NewBufferString("test")
	termIO := &termIO{in: buf}

	p := make([]byte, 4)
	n, err := termIO.Read(p)
	assert.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "test", string(p))
}

func TestTermIO_Write_WithViewport(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	vp := initViewport(ctx, make(chan viewportChanged, 1), 10, 2)
	termIO := &termIO{vp: vp}

	data := []byte("test")
	n, err := termIO.Write(data)
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
}

func TestTermIO_Write_WithoutViewport(t *testing.T) {
	buf := &bytes.Buffer{}
	termIO := &termIO{out: buf}

	data := []byte("test")
	n, err := termIO.Write(data)
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, "test", buf.String())
}

func TestTermIOClearFixedViewportRemovesLines(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	notify := make(chan viewportChanged, 10)
	vp := initViewport(ctx, notify, 10, 2)
	vp.fixedHeight = true
	termio := &termIO{vp: vp}

	_, err := termio.Write([]byte("hello\n"))
	assert.NoError(t, err)
	<-notify

	var buf bytes.Buffer
	_, err = vp.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\rhello\n", buf.String())

	err = termio.clear(1, termio)
	assert.NoError(t, err)
	<-notify

	buf.Reset()
	_, err = vp.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "", buf.String())
}

func TestTermIO_ReadKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected rune
		wantErr  bool
	}{
		{"ctrl-c", "\x03", 0, true},
		{"ctrl-d", "\x04", 0, true},
		{"normal char", "a", 'a', false},
		{"enter", "\x0d", '\x0d', false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := bytes.NewBufferString(tt.input)
			termIO := &termIO{in: buf}

			r, err := termIO.ReadKey()
			if tt.wantErr {
				assert.Error(t, err)
				assert.True(t, errors.Is(err, io.EOF))
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, r)
			}
		})
	}
}

func TestTermIO_ReadRune(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected rune
		wantErr  bool
	}{
		{"up arrow", []byte{0x1b, 0x5b, 0x41}, '↑', false},
		{"down arrow", []byte{0x1b, 0x5b, 0x42}, '↓', false},
		{"right arrow", []byte{0x1b, 0x5b, 0x43}, '→', false},
		{"left arrow", []byte{0x1b, 0x5b, 0x44}, '←', false},
		{"ctrl-c", []byte{0x03}, 0, true},
		{"ctrl-d", []byte{0x04}, 0, true},
		{"normal char", []byte{'a'}, 'a', false},
		{"unknown escape", []byte{0x1b, 0x5b, 0x50}, keyIgnored, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := bytes.NewBuffer(tt.input)
			termIO := &termIO{in: buf}

			r, n, err := termIO.readRune()
			if tt.wantErr {
				assert.Error(t, err)
				assert.True(t, errors.Is(err, io.EOF))
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, r)
				assert.Equal(t, len(tt.input), n)
			}
		})
	}
}

func TestTermIO_ReadRunePasteError(t *testing.T) {
	buf := bytes.NewBufferString("ab")
	termIO := &termIO{in: buf}
	_, _, err := termIO.readRune()
	if err == nil {
		t.Fatalf("expected error")
	}
	var paste *pasteTextError
	if !errors.As(err, &paste) {
		t.Fatalf("expected paste error, got %T", err)
	}
	if paste.Error() == "" {
		t.Fatalf("expected error message")
	}
}

// regression: non-arrow escape input must not absorb the keys that follow it
func TestTermIO_ReadRuneIgnoresNonArrowEscape(t *testing.T) {
	tests := []struct {
		name string
		seq  string
	}{
		{"alt key", "\x1bx"},
		{"delete", "\x1b[3~"},
		{"home", "\x1b[H"},
		{"ss3 function key", "\x1bOP"},
		{"csi with private parameters", "\x1b[<0;1;2M"},
		{"malformed csi", "\x1b[1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rest := "\x03next keys"
			buf := bytes.NewBufferString(tt.seq + rest)
			termIO := &termIO{in: iotest.OneByteReader(buf)}
			r, n, err := termIO.readRune()
			assert.NoError(t, err)
			assert.Equal(t, keyIgnored, r)
			assert.Equal(t, len(tt.seq), n)
			// a byte read past the sequence stays buffered for the next key
			assert.Equal(t, rest, string(termIO.pending)+buf.String())
			_, _, err = termIO.readRune()
			assert.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestTermIO_ReadRuneEscapeBeforeControlKey(t *testing.T) {
	termIO := &termIO{in: iotest.OneByteReader(bytes.NewBufferString("\x1b\x03"))}
	r, n, err := termIO.readRune()
	assert.NoError(t, err)
	assert.Equal(t, keyEscape, r)
	assert.Equal(t, 1, n)
	_, _, err = termIO.readRune()
	assert.ErrorIs(t, err, io.EOF)
}

func TestTermIO_ReadRuneEscapePairs(t *testing.T) {
	termIO := &termIO{in: bytes.NewBufferString("\x1b\x1b[B")}
	r, _, err := termIO.readRune()
	assert.NoError(t, err)
	assert.Equal(t, keyEscape, r)
	r, _, err = termIO.readRune()
	assert.NoError(t, err)
	assert.Equal(t, '↓', r)
}

// regression: a lone Esc must not block until the next key press
func TestTermIO_ReadRuneLoneEscapeTimesOut(t *testing.T) {
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, r.Close())
		assert.NoError(t, w.Close())
	})
	termIO := &termIO{in: r}
	tests := []struct {
		name string
		seq  string
		want rune
	}{
		{"lone escape", "\x1b", keyEscape},
		{"incomplete csi", "\x1b[", keyIgnored},
		{"incomplete ss3", "\x1bO", keyIgnored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := w.WriteString(tt.seq)
			assert.NoError(t, err)
			got, n, err := termIO.readRune()
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, len(tt.seq), n)
			_, err = w.WriteString("q")
			assert.NoError(t, err)
			got, _, err = termIO.readRune()
			assert.NoError(t, err)
			assert.Equal(t, 'q', got)
		})
	}
}

func TestTermIO_ReadRuneSplitArrow(t *testing.T) {
	tests := []struct {
		name string
		seq  string
		want rune
	}{
		{"csi", "\x1b[A", '↑'},
		{"ss3", "\x1bOB", '↓'},
		{"csi with modifier", "\x1b[1;5C", '→'},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := bytes.NewBufferString(tt.seq + "q")
			termIO := &termIO{in: iotest.OneByteReader(buf)}
			r, n, err := termIO.readRune()
			assert.NoError(t, err)
			assert.Equal(t, tt.want, r)
			assert.Equal(t, len(tt.seq), n)
			assert.Equal(t, "q", buf.String())
		})
	}
}

func TestTermIO_ReadRuneMultiByte(t *testing.T) {
	for _, want := range []rune{'é', '日', '🙂'} {
		t.Run(string(want), func(t *testing.T) {
			buf := bytes.NewBufferString(string(want) + "q")
			termIO := &termIO{in: iotest.OneByteReader(buf)}
			r, n, err := termIO.readRune()
			assert.NoError(t, err)
			assert.Equal(t, want, r)
			assert.Equal(t, utf8.RuneLen(want), n)
			assert.Equal(t, "q", buf.String())
		})
	}
}

func TestTermIO_ReadRuneInvalidByte(t *testing.T) {
	termIO := &termIO{in: bytes.NewBuffer([]byte{0xff})}
	r, _, err := termIO.readRune()
	assert.NoError(t, err)
	assert.Equal(t, utf8.RuneError, r)
}

func TestTermIO_ReadRunePasteKeepsRunesWhole(t *testing.T) {
	// the 16-byte read buffer fills in the middle of the trailing "é"
	text := strings.Repeat("a", 15) + "é"
	termIO := &termIO{in: bytes.NewBufferString(text)}
	_, _, err := termIO.readRune()
	var paste *pasteTextError
	assert.True(t, errors.As(err, &paste))
	assert.Equal(t, strings.Repeat("a", 15), string(paste.buf))
	r, n, err := termIO.readRune()
	assert.NoError(t, err)
	assert.Equal(t, 'é', r)
	assert.Equal(t, 2, n)
}

// a rune that never completes must not block reading the next key
func TestTermIO_ReadRuneIncompleteRuneTimesOut(t *testing.T) {
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, r.Close())
		assert.NoError(t, w.Close())
	})
	termIO := &termIO{in: r}
	_, err = w.Write([]byte{0xe2, 0x82})
	assert.NoError(t, err)
	_, n, err := termIO.readRune()
	var paste *pasteTextError
	assert.True(t, errors.As(err, &paste))
	assert.Equal(t, 2, n)
	_, err = w.WriteString("q")
	assert.NoError(t, err)
	got, _, err := termIO.readRune()
	assert.NoError(t, err)
	assert.Equal(t, 'q', got)
}

func TestIsPrintable(t *testing.T) {
	tests := []struct {
		r        rune
		expected bool
	}{
		{' ', true},
		{'a', true},
		{'Z', true},
		{'0', true},
		{'!', true},
		{'\t', false},
		{'\n', false},
		{31, false},
		{0xd800, false}, // surrogate
		{0xdbff, false}, // surrogate
	}

	for _, tt := range tests {
		t.Run(string(tt.r), func(t *testing.T) {
			result := isPrintable(tt.r)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Input ownership follows the reader even when a new prompt changes output.
func TestMakeTermIO_SharedInputAcrossOutputs(t *testing.T) {
	oldSize, oldCheck := termGetSize, terminalInputChecker
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	terminalInputChecker = func(int) bool { return false }
	t.Cleanup(func() { termGetSize, terminalInputChecker = oldSize, oldCheck })
	in := &mockDescriptor{Reader: bytes.NewBufferString("\x1b[A\r"), fd: 0}
	out1 := &mockDescriptor{Writer: &bytes.Buffer{}, fd: 1}
	out2 := &mockDescriptor{Writer: &bytes.Buffer{}, fd: 2}
	first, err := makeTermIO(in, out1)
	assert.NoError(t, err)
	read, _ := first.startRead()
	<-read.done // completed but not yet accepted by the cancelled prompt
	second, err := makeTermIO(in, out2)
	assert.NoError(t, err)
	if first == second || second.out != out2 || first.input != second.input {
		t.Fatal("expected fresh terminal state with shared input")
	}
	adopted, accept := second.startRead()
	if adopted != read {
		t.Fatal("outstanding read was not adopted")
	}
	assert.Equal(t, '↑', adopted.event.key)
	accept()
	third, err := makeTermIO(in, out1)
	assert.NoError(t, err)
	pending, accept := third.startRead()
	<-pending.done
	assert.Equal(t, rune(keyEnter), pending.event.key)
	accept()
	inputStates.Lock()
	_, retained := inputStates.states[in]
	inputStates.Unlock()
	if retained {
		t.Fatal("idle input state retained in registry")
	}
	assert.NoError(t, first.Restore())
	assert.NoError(t, second.Restore())
	assert.NoError(t, third.Restore())
}

func TestSharedInputState_DistinctReadersSameDescriptor(t *testing.T) {
	first := &mockDescriptor{Reader: bytes.NewBufferString("a"), fd: 123}
	second := &mockDescriptor{Reader: bytes.NewBufferString("b"), fd: 123}
	tio := &termIO{in: first, input: sharedInputState(first)}
	read, accept := tio.startRead()
	<-read.done
	if sharedInputState(second) == tio.input {
		t.Fatal("distinct readers shared state")
	}
	if sharedInputState(first) != tio.input {
		t.Fatal("reader identity did not retain state")
	}
	accept()
}

func TestSharedInputState_FileIsLocal(t *testing.T) {
	if sharedInputState(os.Stdin).source != nil {
		t.Fatal("plain files must not enter the shared registry")
	}
}

func TestTermIO_ReadEventsAdoptsUnacceptedRead(t *testing.T) {
	in := &mockDescriptor{Reader: bytes.NewBufferString("\x1b[A\r"), fd: 0}
	first := &termIO{in: in}
	read, _ := first.startRead()
	<-read.done
	second := &termIO{in: in}
	ev := <-second.readEvents(t.Context(), nil)
	second.awaitReader()
	assert.NoError(t, ev.err)
	assert.Equal(t, '↑', ev.key)
	ev = <-second.readEvents(t.Context(), nil)
	second.awaitReader()
	assert.NoError(t, ev.err)
	assert.Equal(t, rune(keyEnter), ev.key)
	ev = <-second.readEvents(t.Context(), nil)
	second.awaitReader()
	assert.True(t, errors.Is(ev.err, io.EOF))
}

func TestDropdownPressKeyReadsSharedPendingEnter(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "same terminal"
		if fresh {
			name = "fresh terminal"
		}
		t.Run(name, func(t *testing.T) {
			in := &mockDescriptor{Reader: bytes.NewBufferString("\x1b[A\r"), fd: 0}
			first := &termIO{in: in, out: &bytes.Buffer{}}
			ev := <-first.readEvents(t.Context(), nil)
			first.awaitReader()
			assert.NoError(t, ev.err)
			assert.Equal(t, '↑', ev.key)
			second := first
			if fresh {
				second = &termIO{in: in, out: &bytes.Buffer{}}
			}
			d := newDropdown()
			d.Items = []any{"one", "two"}
			d.relevant = []int{0, 1}
			i, err := d.pressKey(second, &bytes.Buffer{}, 1, 2)
			assert.NoError(t, err)
			assert.Equal(t, 0, i)
			ev = <-second.readEvents(t.Context(), nil)
			second.awaitReader()
			assert.True(t, errors.Is(ev.err, io.EOF))
		})
	}
}

func TestSharedInputState_UnacceptedCtrlCRetained(t *testing.T) {
	in := &mockDescriptor{Reader: bytes.NewBufferString("\x03"), fd: 0}
	first := &termIO{in: in}
	read, _ := first.startRead()
	<-read.done
	if sharedInputState(in) != first.input {
		t.Fatal("unaccepted Ctrl-C was discarded as an empty failed read")
	}
	second := &termIO{in: in}
	ev := <-second.readEvents(t.Context(), nil)
	second.awaitReader()
	assert.True(t, errors.Is(ev.err, io.EOF))
}

func TestSharedInputState_CancelledClosedReaderReleased(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close()
	defer pw.Close()
	in := &notifyingReader{File: pr, started: make(chan struct{})}
	tio := &termIO{in: in}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tio.readEvents(ctx, nil)
	<-in.started
	cancel()
	tio.awaitReader()
	assert.NoError(t, pr.Close())
	<-tio.input.read.done
	assert.Error(t, tio.input.read.event.err)
	inputStates.Lock()
	_, retained := inputStates.states[in]
	inputStates.Unlock()
	if retained {
		t.Fatal("closed input retained after its cancelled read finished")
	}
}

func TestMakeTermIORestoresRawModeWhenViewportFails(t *testing.T) {
	prevRaw, prevRestore, prevChecker := termMakeRaw, termRestore, terminalInputChecker
	t.Cleanup(func() {
		termMakeRaw, termRestore, terminalInputChecker = prevRaw, prevRestore, prevChecker
	})
	raw := false
	terminalInputChecker = func(int) bool { return true }
	termMakeRaw = func(int) (*term.State, error) {
		raw = true
		return &term.State{}, nil
	}
	restoreErr := errors.New("restore failed")
	termRestore = func(int, *term.State) error {
		raw = false
		return restoreErr
	}
	ctx, cancel := context.WithCancel(t.Context())
	cio := startChanIO(ctx, 80, 24)
	cancel()

	tio, err := makeTermIO(&mockDescriptor{fd: 5}, cio)
	assert.True(t, tio == nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, err, restoreErr)
	assert.True(t, !raw)
}

func TestTermIORestoreReleasesViewportAndLeavesRawModeOnce(t *testing.T) {
	prevRaw, prevRestore, prevChecker := termMakeRaw, termRestore, terminalInputChecker
	t.Cleanup(func() {
		termMakeRaw, termRestore, terminalInputChecker = prevRaw, prevRestore, prevChecker
	})
	var restores int
	terminalInputChecker = func(int) bool { return true }
	termMakeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	termRestore = func(int, *term.State) error {
		restores++
		return nil
	}
	cio := startChanIO(t.Context(), 80, 24)
	tio, err := makeTermIO(&mockDescriptor{fd: 5}, cio)
	assert.NoError(t, err)

	// output-only widgets leave raw mode but keep drawing
	assert.NoError(t, tio.restoreMode())
	assert.Equal(t, 1, restores)
	assert.Equal(t, 1, managedViewports(cio))
	_, err = tio.Write([]byte("still drawing"))
	assert.NoError(t, err)

	assert.NoError(t, tio.Restore())
	assert.NoError(t, tio.Restore())
	assert.Equal(t, 1, restores) // must not undo raw mode a later prompt entered
	assert.Equal(t, 0, managedViewports(cio))
}
