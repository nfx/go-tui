// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"bytes"
	"context"
	"errors"
	"github.com/nfx/go-tui/internal/errs"
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
	assert.ErrorIs(t, err, errs.ErrNoTTY)
}

func TestMakeTermIO_NoDescriptorInput(t *testing.T) {
	in := &mockReader{}
	out := &mockDescriptor{fd: 1}

	_, err := makeTermIO(in, out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stdin")
	assert.ErrorIs(t, err, errs.ErrNoTTY)
}

func TestMakeTermIO_WithChanIO(t *testing.T) {
	cio, _ := chainIOforTest(t, 5)
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
	if !reflect.DeepEqual(termio.In, in) {
		t.Fatalf("unexpected stdin")
	}
	if termio.Width != cio.Width {
		t.Fatalf("unexpected width")
	}
	if termio.Restore == nil {
		t.Fatalf("restore not set")
	}
	err = termio.Restore()
	assert.NoError(t, err)
}

func TestMakeTermIO_WithChanIO_RawTTYInput(t *testing.T) {
	cio, _ := chainIOforTest(t, 5)
	origCheck := InputChecker
	origRaw := MakeRaw
	origRestore := Restore
	InputChecker = func(int) bool { return true }
	MakeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	Restore = func(int, *term.State) error { return nil }
	t.Cleanup(func() {
		InputChecker = origCheck
		MakeRaw = origRaw
		Restore = origRestore
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
	cio, _ := chainIOforTest(t, 5)
	origCheck := InputChecker
	origRaw := MakeRaw
	InputChecker = func(int) bool { return true }
	MakeRaw = func(int) (*term.State, error) { return nil, io.EOF }
	t.Cleanup(func() {
		InputChecker = origCheck
		MakeRaw = origRaw
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
	assert.Equal(t, in, termIO.In)
	assert.Equal(t, out, termIO.Out)
	assert.True(t, termIO.Width > 0)
	assert.True(t, termIO.Height > 0)
	assert.NotNil(t, termIO.Restore)

	// Clean up
	err = termIO.Restore()
	assert.NoError(t, err)
}

func TestMakeTermIO_WithDescriptorStubbed(t *testing.T) {
	origGet := GetSize
	origRaw := MakeRaw
	origRestore := Restore
	GetSize = func(int) (int, int, error) { return 80, 24, nil }
	MakeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	Restore = func(int, *term.State) error { return nil }
	t.Cleanup(func() {
		GetSize = origGet
		MakeRaw = origRaw
		Restore = origRestore
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
	origGet := GetSize
	origRaw := MakeRaw
	origCheck := InputChecker
	GetSize = func(int) (int, int, error) { return 80, 24, nil }
	MakeRaw = func(int) (*term.State, error) { return nil, io.EOF }
	InputChecker = func(int) bool { return true }
	t.Cleanup(func() {
		GetSize = origGet
		MakeRaw = origRaw
		InputChecker = origCheck
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
	termIO := &IO{In: buf}

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
	termIO := &IO{Vp: vp}

	data := []byte("test")
	n, err := termIO.Write(data)
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
}

func TestTermIO_Write_WithoutViewport(t *testing.T) {
	buf := &bytes.Buffer{}
	termIO := &IO{Out: buf}

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
	vp.FixedHeight = true
	termio := &IO{Vp: vp}

	_, err := termio.Write([]byte("hello\n"))
	assert.NoError(t, err)
	<-notify

	var buf bytes.Buffer
	_, err = vp.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\rhello\n", buf.String())

	err = termio.Clear(1, termio)
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
			termIO := &IO{In: buf}

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
		{"unknown escape", []byte{0x1b, 0x5b, 0x50}, KeyIgnored, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := bytes.NewBuffer(tt.input)
			termIO := &IO{In: buf}

			r, n, err := termIO.ReadRuneCtx(t.Context())
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
	termIO := &IO{In: buf}
	_, _, err := termIO.ReadRuneCtx(t.Context())
	if err == nil {
		t.Fatalf("expected error")
	}
	var paste *PasteTextError
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
			termIO := &IO{In: iotest.OneByteReader(buf)}
			r, n, err := termIO.ReadRuneCtx(t.Context())
			assert.NoError(t, err)
			assert.Equal(t, KeyIgnored, r)
			assert.Equal(t, len(tt.seq), n)
			// a byte read past the sequence stays buffered for the next key
			assert.Equal(t, rest, string(termIO.Pending)+buf.String())
			_, _, err = termIO.ReadRuneCtx(t.Context())
			assert.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestTermIO_ReadRuneEscapeBeforeControlKey(t *testing.T) {
	termIO := &IO{In: iotest.OneByteReader(bytes.NewBufferString("\x1b\x03"))}
	r, n, err := termIO.ReadRuneCtx(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, KeyEscape, r)
	assert.Equal(t, 1, n)
	_, _, err = termIO.ReadRuneCtx(t.Context())
	assert.ErrorIs(t, err, io.EOF)
}

func TestTermIO_ReadRuneEscapePairs(t *testing.T) {
	termIO := &IO{In: bytes.NewBufferString("\x1b\x1b[B")}
	r, _, err := termIO.ReadRuneCtx(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, KeyEscape, r)
	r, _, err = termIO.ReadRuneCtx(t.Context())
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
	termIO := &IO{In: r}
	tests := []struct {
		name string
		seq  string
		want rune
	}{
		{"lone escape", "\x1b", KeyEscape},
		{"incomplete csi", "\x1b[", KeyIgnored},
		{"incomplete ss3", "\x1bO", KeyIgnored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := w.WriteString(tt.seq)
			assert.NoError(t, err)
			got, n, err := termIO.ReadRuneCtx(t.Context())
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, len(tt.seq), n)
			_, err = w.WriteString("q")
			assert.NoError(t, err)
			got, _, err = termIO.ReadRuneCtx(t.Context())
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
			termIO := &IO{In: iotest.OneByteReader(buf)}
			r, n, err := termIO.ReadRuneCtx(t.Context())
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
			termIO := &IO{In: iotest.OneByteReader(buf)}
			r, n, err := termIO.ReadRuneCtx(t.Context())
			assert.NoError(t, err)
			assert.Equal(t, want, r)
			assert.Equal(t, utf8.RuneLen(want), n)
			assert.Equal(t, "q", buf.String())
		})
	}
}

func TestTermIO_ReadRuneInvalidByte(t *testing.T) {
	termIO := &IO{In: bytes.NewBuffer([]byte{0xff})}
	r, _, err := termIO.ReadRuneCtx(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, utf8.RuneError, r)
}

func TestTermIO_ReadRunePasteKeepsRunesWhole(t *testing.T) {
	// the 16-byte read buffer fills in the middle of the trailing "é"
	text := strings.Repeat("a", 15) + "é"
	termIO := &IO{In: bytes.NewBufferString(text)}
	_, _, err := termIO.ReadRuneCtx(t.Context())
	var paste *PasteTextError
	assert.True(t, errors.As(err, &paste))
	assert.Equal(t, strings.Repeat("a", 15), string(paste.Buf))
	r, n, err := termIO.ReadRuneCtx(t.Context())
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
	termIO := &IO{In: r}
	_, err = w.Write([]byte{0xe2, 0x82})
	assert.NoError(t, err)
	_, n, err := termIO.ReadRuneCtx(t.Context())
	var paste *PasteTextError
	assert.True(t, errors.As(err, &paste))
	assert.Equal(t, 2, n)
	_, err = w.WriteString("q")
	assert.NoError(t, err)
	got, _, err := termIO.ReadRuneCtx(t.Context())
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
	oldSize, oldCheck := GetSize, InputChecker
	GetSize = func(int) (int, int, error) { return 80, 24, nil }
	InputChecker = func(int) bool { return false }
	t.Cleanup(func() { GetSize, InputChecker = oldSize, oldCheck })
	in := &mockDescriptor{Reader: bytes.NewBufferString("\x1b[A\r"), fd: 0}
	out1 := &mockDescriptor{Writer: &bytes.Buffer{}, fd: 1}
	out2 := &mockDescriptor{Writer: &bytes.Buffer{}, fd: 2}
	first, err := makeTermIO(in, out1)
	assert.NoError(t, err)
	read, _ := first.StartRead(t.Context())
	<-read.Done // completed but not yet accepted by the cancelled prompt
	second, err := makeTermIO(in, out2)
	assert.NoError(t, err)
	if first == second || second.Out != out2 || first.Input != second.Input {
		t.Fatal("expected fresh terminal state with shared input")
	}
	adopted, accept := second.StartRead(t.Context())
	if adopted != read {
		t.Fatal("outstanding read was not adopted")
	}
	assert.Equal(t, '↑', adopted.Event.Key)
	accept()
	third, err := makeTermIO(in, out1)
	assert.NoError(t, err)
	pending, accept := third.StartRead(t.Context())
	<-pending.Done
	assert.Equal(t, rune(KeyEnter), pending.Event.Key)
	accept()
	InputStates.Lock()
	_, retained := InputStates.States[in]
	InputStates.Unlock()
	if retained {
		t.Fatal("idle input state retained in registry")
	}
	assert.NoError(t, first.Restore())
	assert.NoError(t, second.Restore())
	assert.NoError(t, third.Restore())
}

func TestTermIO_ReadEventsAdoptsUnacceptedRead(t *testing.T) {
	in := &mockDescriptor{Reader: bytes.NewBufferString("\x1b[A\r"), fd: 0}
	first := &IO{In: in}
	read, _ := first.StartRead(t.Context())
	<-read.Done
	second := &IO{In: in}
	ev := <-second.ReadEvents(t.Context(), nil)
	second.AwaitReader()
	assert.NoError(t, ev.Err)
	assert.Equal(t, '↑', ev.Key)
	ev = <-second.ReadEvents(t.Context(), nil)
	second.AwaitReader()
	assert.NoError(t, ev.Err)
	assert.Equal(t, rune(KeyEnter), ev.Key)
	ev = <-second.ReadEvents(t.Context(), nil)
	second.AwaitReader()
	assert.ErrorIs(t, ev.Err, io.EOF)
}

func TestMakeTermIORestoresRawModeWhenViewportFails(t *testing.T) {
	prevRaw, prevRestore, prevChecker := MakeRaw, Restore, InputChecker
	t.Cleanup(func() {
		MakeRaw, Restore, InputChecker = prevRaw, prevRestore, prevChecker
	})
	raw := false
	InputChecker = func(int) bool { return true }
	MakeRaw = func(int) (*term.State, error) {
		raw = true
		return &term.State{}, nil
	}
	restoreErr := errors.New("restore failed")
	Restore = func(int, *term.State) error {
		raw = false
		return restoreErr
	}
	ctx, cancel := context.WithCancel(t.Context())
	cio := startChanIO(ctx)
	cancel()

	tio, err := makeTermIO(&mockDescriptor{fd: 5}, cio)
	assert.True(t, tio == nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, err, restoreErr)
	assert.True(t, !raw)
}

func TestTermIORestoreReleasesViewportAndLeavesRawModeOnce(t *testing.T) {
	prevRaw, prevRestore, prevChecker := MakeRaw, Restore, InputChecker
	t.Cleanup(func() {
		MakeRaw, Restore, InputChecker = prevRaw, prevRestore, prevChecker
	})
	var restores int
	InputChecker = func(int) bool { return true }
	MakeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	Restore = func(int, *term.State) error {
		restores++
		return nil
	}
	cio := startChanIO(t.Context())
	tio, err := makeTermIO(&mockDescriptor{fd: 5}, cio)
	assert.NoError(t, err)

	// output-only widgets leave raw mode but keep drawing
	assert.NoError(t, tio.RestoreMode())
	assert.Equal(t, 1, restores)
	assert.Equal(t, 1, managedViewports(cio))
	_, err = tio.Write([]byte("still drawing"))
	assert.NoError(t, err)

	assert.NoError(t, tio.Restore())
	assert.NoError(t, tio.Restore())
	assert.Equal(t, 1, restores) // must not undo raw mode a later prompt entered
	assert.Equal(t, 0, managedViewports(cio))
}
