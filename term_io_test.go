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
	assert.ErrorIs(t, err, ErrNoTTY)
}

func TestMakeTermIO_NoDescriptorInput(t *testing.T) {
	in := &mockReader{}
	out := &mockDescriptor{fd: 1}

	_, err := makeTermIO(in, out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stdin")
	assert.ErrorIs(t, err, ErrNoTTY)
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
	if !term.IsTerminal(int(os.Stdout.Fd())) {
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
				assert.ErrorIs(t, err, io.EOF)
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

			r, n, err := termIO.readRune(t.Context())
			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, io.EOF)
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
	_, _, err := termIO.readRune(t.Context())
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
			r, n, err := termIO.readRune(t.Context())
			assert.NoError(t, err)
			assert.Equal(t, keyIgnored, r)
			assert.Equal(t, len(tt.seq), n)
			// a byte read past the sequence stays buffered for the next key
			assert.Equal(t, rest, string(termIO.pending)+buf.String())
			_, _, err = termIO.readRune(t.Context())
			assert.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestTermIO_ReadRuneEscapeBeforeControlKey(t *testing.T) {
	termIO := &termIO{in: iotest.OneByteReader(bytes.NewBufferString("\x1b\x03"))}
	r, n, err := termIO.readRune(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, keyEscape, r)
	assert.Equal(t, 1, n)
	_, _, err = termIO.readRune(t.Context())
	assert.ErrorIs(t, err, io.EOF)
}

func TestTermIO_ReadRuneEscapePairs(t *testing.T) {
	termIO := &termIO{in: bytes.NewBufferString("\x1b\x1b[B")}
	r, _, err := termIO.readRune(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, keyEscape, r)
	r, _, err = termIO.readRune(t.Context())
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
			got, n, err := termIO.readRune(t.Context())
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, len(tt.seq), n)
			_, err = w.WriteString("q")
			assert.NoError(t, err)
			got, _, err = termIO.readRune(t.Context())
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
			r, n, err := termIO.readRune(t.Context())
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
			r, n, err := termIO.readRune(t.Context())
			assert.NoError(t, err)
			assert.Equal(t, want, r)
			assert.Equal(t, utf8.RuneLen(want), n)
			assert.Equal(t, "q", buf.String())
		})
	}
}

func TestTermIO_ReadRuneInvalidByte(t *testing.T) {
	termIO := &termIO{in: bytes.NewBuffer([]byte{0xff})}
	r, _, err := termIO.readRune(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, utf8.RuneError, r)
}

func TestTermIO_ReadRunePasteKeepsRunesWhole(t *testing.T) {
	// the 16-byte read buffer fills in the middle of the trailing "é"
	text := strings.Repeat("a", 15) + "é"
	termIO := &termIO{in: bytes.NewBufferString(text)}
	_, _, err := termIO.readRune(t.Context())
	var paste *pasteTextError
	assert.True(t, errors.As(err, &paste))
	assert.Equal(t, strings.Repeat("a", 15), string(paste.buf))
	r, n, err := termIO.readRune(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 'é', r)
	assert.Equal(t, 2, n)
}

// a rune that never completes must not block reading the next key.
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
	_, n, err := termIO.readRune(t.Context())
	var paste *pasteTextError
	assert.True(t, errors.As(err, &paste))
	assert.Equal(t, 2, n)
	_, err = w.WriteString("q")
	assert.NoError(t, err)
	got, _, err := termIO.readRune(t.Context())
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
	read, _ := first.startRead(t.Context())
	<-read.done // completed but not yet accepted by the cancelled prompt
	second, err := makeTermIO(in, out2)
	assert.NoError(t, err)
	if first == second || second.out != out2 || first.input != second.input {
		t.Fatal("expected fresh terminal state with shared input")
	}
	adopted, accept := second.startRead(t.Context())
	if adopted != read {
		t.Fatal("outstanding read was not adopted")
	}
	assert.Equal(t, '↑', adopted.event.key)
	accept()
	third, err := makeTermIO(in, out1)
	assert.NoError(t, err)
	pending, accept := third.startRead(t.Context())
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
	read, accept := tio.startRead(t.Context())
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
	read, _ := first.startRead(t.Context())
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
	assert.ErrorIs(t, ev.err, io.EOF)
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
			d := newDropdown(t.Context())
			d.Items = []any{"one", "two"}
			d.relevant = []int{0, 1}
			i, err := d.pressKey(d.Ctx, second, &bytes.Buffer{}, 1, 2)
			assert.NoError(t, err)
			assert.Equal(t, 0, i)
			ev = <-second.readEvents(t.Context(), nil)
			second.awaitReader()
			assert.ErrorIs(t, ev.err, io.EOF)
		})
	}
}

func TestSharedInputState_UnacceptedCtrlCRetained(t *testing.T) {
	in := &mockDescriptor{Reader: bytes.NewBufferString("\x03"), fd: 0}
	first := &termIO{in: in}
	read, _ := first.startRead(t.Context())
	<-read.done
	if sharedInputState(in) != first.input {
		t.Fatal("unaccepted Ctrl-C was discarded as an empty failed read")
	}
	second := &termIO{in: in}
	ev := <-second.readEvents(t.Context(), nil)
	second.awaitReader()
	assert.ErrorIs(t, ev.err, io.EOF)
}

func TestSharedInputState_CancelledClosedReaderReleased(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
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

func TestInput_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	value, err := Input(context.Background(), "Name",
		WithInput(strings.NewReader("alice\r")), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "alice", value)
	assert.Contains(t, out.String(), "Name")
}

func TestDropdown_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	// Down, Enter
	value, err := Dropdown(context.Background(), "Pick", []string{"a", "b", "c"},
		WithInput(strings.NewReader("\x1b[B\r")), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "b", value)
}

func TestConfirm_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	ok := Confirm(context.Background(), "Proceed",
		WithInput(strings.NewReader("\r")), WithOutput(&out))
	assert.True(t, ok)
}

func TestInput_ScriptedInputDefaultOutput(t *testing.T) {
	value, err := Input(context.Background(), "Name", WithInput(strings.NewReader("bob\r")), WithOutput(io.Discard))
	assert.NoError(t, err)
	assert.Equal(t, "bob", value)
}

func TestHostsTerminal(t *testing.T) {
	assert.True(t, !hostsTerminal(&bytes.Buffer{}))
	assert.True(t, !hostsTerminal(io.Discard))
	f, err := os.CreateTemp(t.TempDir(), "out")
	assert.NoError(t, err)
	defer f.Close() //nolint:errcheck
	assert.True(t, !hostsTerminal(f))
	assert.True(t, hostsTerminal(startChanIO(t.Context(), 80, 24)))
}

func TestRoutingWrapperInputBufferOutput(t *testing.T) {
	strictOutput(t)
	var out bytes.Buffer
	got, err := Input(t.Context(), "Name",
		WithInput(strings.NewReader("typed\r")), WithOutput(&out), WithDefault("dflt"))
	assert.NoError(t, err)
	assert.Equal(t, "dflt", got)
	assert.Equal(t, "", out.String())

	_, err = Input(t.Context(), "Name", WithInput(strings.NewReader("typed\r")), WithOutput(&out))
	assert.ErrorIs(t, err, ErrNoTTY)

	f, ferr := os.CreateTemp(t.TempDir(), "out")
	assert.NoError(t, ferr)
	defer f.Close() //nolint:errcheck
	_, err = Dropdown(t.Context(), "Pick", []string{"a"}, WithOutput(f), WithInput(f))
	assert.ErrorIs(t, err, ErrNoTTY)
}

func TestInteractiveStillWorksOnManagedOutput(t *testing.T) {
	// production classification: a managed terminal with scripted input
	strictOutput(t)
	cio := startChanIO(t.Context(), 80, 24)
	got, err := Input(t.Context(), "Name", WithInput(strings.NewReader("bob\r")), WithOutput(cio))
	if errors.Is(err, ErrNoTTY) {
		t.Fatal("managed terminal must stay interactive")
	}
	_ = got
}

func TestScanControlFrame(t *testing.T) {
	invalid := controlFrame{isInvalid: true}
	for name, tc := range map[string]struct {
		in   string
		want controlFrame
	}{
		"bel":         {"\x1b]11;rgb:ffff/0000/8080\x07", controlFrame{size: 24, isComplete: true, reply: controlReply{11, "rgb:ffff/0000/8080"}}},
		"st":          {"\x1b]11;rgb:ff/00/80\x1b\\", controlFrame{size: 19, isComplete: true, reply: controlReply{11, "rgb:ff/00/80"}}},
		"trailing":    {"\x1b]11;x\x07a", controlFrame{size: 7, isComplete: true, reply: controlReply{11, "x"}}},
		"empty data":  {"\x1b]4;\x07", controlFrame{size: 5, isComplete: true, reply: controlReply{4, ""}}},
		"alt bracket": {"\x1b]", controlFrame{size: 2, isIncomplete: true}},
		"code only":   {"\x1b]11", controlFrame{size: 4, isIncomplete: true}},
		"no end":      {"\x1b]11;rgb:", controlFrame{size: 9, isIncomplete: true}},
		"lone esc":    {"\x1b]11;x\x1b", controlFrame{size: 7, isIncomplete: true}},
		"no code":     {"\x1b];x\x07", invalid},
		"letter code": {"\x1b]a;x\x07", invalid},
		"long code":   {"\x1b]1111;x\x07", invalid},
		"control":     {"\x1b]11;a\rb\x07", invalid},
		"bad esc":     {"\x1b]11;a\x1b[A\x07", invalid},
		"too long":    {"\x1b]11;" + strings.Repeat("a", maxControlFrame) + "\x07", invalid},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, new(termIO).scanControlFrame([]byte(tc.in)))
		})
	}
}

// oscReply is what a terminal sends for an OSC 11 query.
const oscReply = "\x1b]11;rgb:1e1e/1e1e/2e2e\x07"

// scriptedKeys reads keys from in.
func scriptedKeys(in io.Reader) *termIO {
	return &termIO{in: &mockDescriptor{Reader: in, fd: 0}, splitKeys: true}
}

// nextEvent reads one event and waits for its reader to finish.
func nextEvent(t *testing.T, tio *termIO) keyEvent {
	t.Helper()
	ev := <-tio.readEvents(t.Context(), nil)
	tio.awaitReader()
	return ev
}

// readKeys reads events until EOF and returns the keys that are not ignored.
func readKeys(t *testing.T, tio *termIO) string {
	t.Helper()
	var keys strings.Builder
	for {
		ev := nextEvent(t, tio)
		if ev.err != nil {
			assert.ErrorIs(t, ev.err, io.EOF)
			return keys.String()
		}
		if ev.key != keyIgnored {
			keys.WriteRune(ev.key)
		}
	}
}

func TestTermIO_ControlReplyIsNotAKey(t *testing.T) {
	for name, tc := range map[string]struct{ in, keys string }{
		"alone then key": {oscReply + "a", "a"},
		"key then reply": {"a" + oscReply, "a"},
		"coalesced":      {"a" + oscReply + "b", "ab"},
		"st terminator":  {"\x1b]11;rgb:0/0/0\x1b\\a", "a"},
		"arrow after":    {oscReply + "\x1b[A", "↑"},
		"two replies":    {oscReply + oscReply + "a", "a"},
	} {
		t.Run(name, func(t *testing.T) {
			tio := scriptedKeys(bytes.NewBufferString(tc.in))
			q := tio.expectControl(11)
			defer q.cancel()
			assert.Equal(t, tc.keys, readKeys(t, tio))
			assert.Equal(t, 1, len(q.reply))
			assert.Equal(t, 11, (<-q.reply).code)
		})
	}
}

func TestTermIO_ControlReplyKeepsKeyPermit(t *testing.T) {
	tio := scriptedKeys(bytes.NewBufferString(oscReply + "a"))
	permits := make(chan struct{}, 1)
	permits <- struct{}{} // a single key may be read
	ev := <-tio.readEvents(t.Context(), permits)
	assert.NoError(t, ev.err)
	assert.Equal(t, 'a', ev.key)
}

func TestTermIO_ControlReplySplitAcrossReads(t *testing.T) {
	tio := scriptedKeys(iotest.OneByteReader(strings.NewReader("\x1b]11;rgb:0000/0000/0000\x1b\\q")))
	q := tio.expectControl(11)
	assert.Equal(t, 'q', nextEvent(t, tio).key)
	assert.Equal(t, "rgb:0000/0000/0000", (<-q.reply).data)
}

func TestTermIO_LateControlReplyIsDropped(t *testing.T) {
	tio := scriptedKeys(bytes.NewBufferString(oscReply + "a"))
	tio.expectControl(11).cancel() // the query already timed out
	assert.Equal(t, 'a', nextEvent(t, tio).key)
}

func TestTermIO_ControlReplyWrongCodeStaysQueued(t *testing.T) {
	tio := scriptedKeys(bytes.NewBufferString(oscReply + "a"))
	q := tio.expectControl(10)
	defer q.cancel()
	assert.Equal(t, 'a', nextEvent(t, tio).key)
	assert.Equal(t, 0, len(q.reply)) // a reply for another code is not delivered
}

func TestTermIO_AltBracketStillAKey(t *testing.T) {
	tio := &termIO{}
	n, complete := tio.escapeSequenceLen([]byte("\x1b]"))
	assert.Equal(t, 2, n)
	assert.Equal(t, false, complete) // waits [escapeTimeout] for a header, then it is Alt+]
	key, consumed, err := tio.decodeRuneBytes([]byte("\x1b]"), 2)
	assert.NoError(t, err)
	assert.Equal(t, rune(keyIgnored), key)
	assert.Equal(t, 2, consumed)
}

func TestTermIO_InvalidOSCIsNotSwallowed(t *testing.T) {
	// not a reply (no numeric code), so only Alt+] is dropped and the text stays typed
	tio := scriptedKeys(bytes.NewBufferString("\x1b]xy"))
	assert.Equal(t, "xy", readKeys(t, tio))
}

func TestTermIO_PasteWithControlReplyStaysPaste(t *testing.T) {
	in := "\x1b[200~" + oscReply + "\x1b[201~"
	tio := &termIO{in: &mockDescriptor{Reader: bytes.NewBufferString(in), fd: 0}, bracketedPaste: true}
	q := tio.expectControl(11)
	defer q.cancel()
	assert.Equal(t, oscReply, string(nextEvent(t, tio).paste))
	assert.Equal(t, 0, len(q.reply)) // the paste payload is not routed as a reply
}

func TestChanIO_WriteControlIsVerbatimAndNotReplayed(t *testing.T) {
	out := &syncBuffer{}
	cio := startChanIOTo(t.Context(), out, 80, 24)

	vp, err := cio.pushViewport()
	assert.NoError(t, err)
	_, err = vp.Write([]byte("prompt"))
	assert.NoError(t, err)
	assert.NoError(t, cio.Flush())

	const query = "\x1b]11;?\x07"
	assert.NoError(t, cio.writeControl(t.Context(), []byte(query)))
	assert.Equal(t, 1, strings.Count(out.String(), query))

	// redraws of the managed rows must not repeat the request
	_, err = vp.Write([]byte(" more"))
	assert.NoError(t, err)
	assert.NoError(t, cio.Flush())
	cio.releaseViewport(vp)
	assert.NoError(t, cio.Flush())
	assert.Equal(t, 1, strings.Count(out.String(), query))
}

func TestChanIO_WriteControlNotHeldBackByPartialLine(t *testing.T) {
	out := &syncBuffer{}
	cio := startChanIOTo(t.Context(), out, 80, 24)
	vp, err := cio.pushViewport()
	assert.NoError(t, err)
	defer cio.releaseViewport(vp)
	_, err = cio.Write([]byte("no newline yet"))
	assert.NoError(t, err)
	assert.NoError(t, cio.writeControl(t.Context(), []byte("\x1b]11;?\x07")))
	assert.Contains(t, out.String(), "\x1b]11;?\x07")
}

func TestChanIO_WriteControlCancelled(t *testing.T) {
	cio := newUnstartedIO(t.Context(), 80, 24, 0) // nothing serves the channel
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, cio.writeControl(ctx, []byte("x")), context.Canceled)
}

func TestTermIO_WriteControl(t *testing.T) {
	out := &bytes.Buffer{}
	direct := &termIO{out: out, fd: 2}
	assert.NoError(t, direct.writeControl(t.Context(), []byte("q")))
	assert.Equal(t, "q", out.String())

	scripted := &termIO{out: out}
	assert.ErrorIs(t, scripted.writeControl(t.Context(), []byte("q")), ErrNoTTY)
	assert.Equal(t, "q", out.String())
}
