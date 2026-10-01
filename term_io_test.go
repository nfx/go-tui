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
	"testing"

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
		{"unknown escape", []byte{0x1b, 0x5b, 0x50}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := bytes.NewBuffer(tt.input)
			termIO := &termIO{in: buf}

			r, n, err := termIO.ReadRune()
			if tt.wantErr {
				if tt.name == "unknown escape" {
					assert.Error(t, err)
				} else {
					assert.Error(t, err)
					assert.True(t, errors.Is(err, io.EOF))
				}
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
	_, _, err := termIO.ReadRune()
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
