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
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/assert"
)

// bindInput prepares a literal input as run does: the installed theme bound to tt.
func bindInput(t *testing.T, i *input, tt *termIO) {
	t.Helper()
	if i.theme == nil {
		i.theme = defaultIO.currentTheme()
	}
	assert.NoError(t, i.bind(t.Context(), tt))
}

// literalInputs render the bare label and answer on an Inline theme into
// plain writers, like the inputs before themes.
func literalInputs(t *testing.T) {
	t.Helper()
	withEnv(t)
	th := NewTheme()
	th.Inline = true
	th.Templates.Input.Label = `{{ .Label }}`
	th.Templates.Input.Answer = `{{ .Value }}`
	useTheme(t, th)
}

func TestRenderNormal(t *testing.T) {
	literalInputs(t)
	i := &input{
		Label:  "Enter text:",
		typed:  "hello",
		cursor: 5,
	}

	var out bytes.Buffer
	io := &termIO{out: &out}
	bindInput(t, i, io)

	frame := &bytes.Buffer{}
	err := i.render(io, frame)
	assert.NoError(t, err)

	expected := "\rEnter text: hello\x1b[K"
	assert.Equal(t, expected, out.String())
}

func TestRenderCursorAtStart(t *testing.T) {
	literalInputs(t)
	i := &input{
		Label:  "Input:",
		typed:  "abc",
		cursor: 0,
	}

	var out bytes.Buffer
	io := &termIO{out: &out}
	bindInput(t, i, io)

	frame := &bytes.Buffer{}
	err := i.render(io, frame)
	assert.NoError(t, err)

	expected := "\rInput: abc\x1b[K\x1b[3D"
	assert.Equal(t, expected, out.String())
}

func TestRenderWriteError(t *testing.T) {
	literalInputs(t)
	i := &input{
		Label:  "Test:",
		typed:  "test",
		cursor: 0,
	}

	// Mock a failing writer
	failingWriter := &failingBuffer{}
	tio := &termIO{out: failingWriter}
	bindInput(t, i, tio)

	frame := &bytes.Buffer{}
	err := i.render(tio, frame)
	assert.Error(t, err)
	assert.ErrorIs(t, err, io.EOF)
}

// Helper struct for testing write errors.
type failingBuffer struct{}

func (f *failingBuffer) Write(p []byte) (n int, err error) {
	return 0, io.EOF
}

func TestInputWithDefault(t *testing.T) {
	i := newInput(t.Context(), "label")
	err := WithDefault("abc")(i)
	assert.NoError(t, err)
	assert.Equal(t, "abc", i.typed)
	assert.Equal(t, 3, i.cursor)
}

func TestInputWithDefaultCountsRunes(t *testing.T) {
	i := newInput(t.Context(), "label")
	err := WithDefault("héllo")(i)
	assert.NoError(t, err)
	assert.Equal(t, 5, i.cursor)
}

func TestInputWithNonEmpty(t *testing.T) {
	i := newInput(t.Context(), "label")
	err := WithNonEmpty()(i)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(i.rules))
}

func TestInputShowAnswerVisibility(t *testing.T) {
	literalInputs(t)
	i := newInput(t.Context(), "Name")
	i.typed = "value"
	var out bytes.Buffer
	i.out = &out
	bindInput(t, i, &termIO{out: &out})

	err := i.showAnswer()
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "value")

	out.Reset()
	i.Hide = true
	err = i.showAnswer()
	assert.NoError(t, err)
	assert.Equal(t, "", out.String())

	i.Hide = false
	i.Password = true
	err = i.showAnswer()
	assert.NoError(t, err)
	assert.Equal(t, "", out.String())
}

func TestInputReadOptionError(t *testing.T) {
	_, err := Input(t.Context(), "Label", func(any) error { return io.EOF })
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestInputReadRunError(t *testing.T) {
	_, err := Input(t.Context(), "Label", WithOutput(io.Discard), opT(func(i *input) error {
		i.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) {
			return nil, io.EOF
		}
		return nil
	}))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestInputOptWrongType(t *testing.T) {
	err := opT(func(*input) error { return nil })("nope")
	if !errors.Is(err, ErrWrongWidget) {
		t.Fatalf("expected ErrWrongWidget, got %v", err)
	}
}

func (p *input) pressKey(tio *termIO) (string, error) {
	ev, ok := <-tio.readEvents(context.Background(), nil)
	tio.awaitReader()
	done, err := p.handleKeyEvent(p.ctx, ev, ok)
	if err != nil {
		return "", err
	}
	if done {
		return p.typed, nil
	}
	return "", nil
}

func TestInputPressKeyBackspace(t *testing.T) {
	i := &input{
		typed:  "ab",
		cursor: 2,
	}
	tio := &termIO{in: bytes.NewBuffer([]byte{0x7f})}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "a", i.typed)
	assert.Equal(t, 1, i.cursor)
}

func TestInputPressKeyArrowLeft(t *testing.T) {
	i := &input{
		typed:  "ab",
		cursor: 2,
	}
	tio := &termIO{in: bytes.NewBuffer([]byte{0x1b, 0x5b, 0x44})}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, 1, i.cursor)
}

func TestInputPressKeyEOF(t *testing.T) {
	i := &input{typed: ""}
	tio := &termIO{in: bytes.NewBuffer(nil)}
	_, err := i.pressKey(tio)
	assert.Error(t, err)
}

func TestInputPressKeyEnter(t *testing.T) {
	i := &input{typed: "done"}
	tio := &termIO{in: bytes.NewBuffer([]byte{keyEnter})}
	out, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "done", out)
}

func TestInputPressKeyAddsChar(t *testing.T) {
	i := &input{typed: "a", cursor: 1}
	tio := &termIO{in: bytes.NewBuffer([]byte{'b'})}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "ab", i.typed)
	assert.Equal(t, 2, i.cursor)
}

func TestInputPressKeyPaste(t *testing.T) {
	i := &input{}
	tio := &termIO{in: bytes.NewBufferString("ab")}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	runes := []rune(i.typed)
	if len(runes) < 2 || runes[0] != 'a' || runes[1] != 'b' {
		t.Fatalf("unexpected paste result %q", i.typed)
	}
}

func TestInputPressKeyDoesNotEmitLowLevelOutputEvents(t *testing.T) {
	i := &input{
		Label:  "Label",
		typed:  "a",
		cursor: 1,
	}
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	tio := &termIO{in: bytes.NewBuffer([]byte{'b'})}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(events))
}

func TestInputPressKeyEnterEmitsCompletedEvent(t *testing.T) {
	i := &input{
		Label:  "Label",
		typed:  "done",
		cursor: 4,
	}
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	tio := &termIO{in: bytes.NewBuffer([]byte{keyEnter})}
	out, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "done", out)
	assert.Equal(t, 1, len(events))
	completed, ok := events[0].(inputComplete)
	assert.True(t, ok)
	assert.Equal(t, "done", completed.Value)
}

func TestInputPressKeyEnterWithNonEmptyRejectsEmpty(t *testing.T) {
	i := newInput(t.Context(), "Label")
	assert.NoError(t, WithNonEmpty()(i))
	tio := &termIO{in: bytes.NewBuffer([]byte{keyEnter})}
	out, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "", out)
	assert.Equal(t, "", i.typed)
}

func TestInputRunEmitsLifecycleEvents(t *testing.T) {
	i := newInput(t.Context(), "Label")
	i.in = &chunkReader{
		chunks: [][]byte{
			{'x'},
			{keyEnter},
		},
	}
	var out bytes.Buffer
	i.out = &out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	got, err := i.run(i.ctx)
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
	assert.Equal(t, 2, len(events))
	_, ok := events[0].(inputInit)
	assert.True(t, ok)
	_, ok = events[1].(inputComplete)
	assert.True(t, ok)
}

func TestInputRunConsumesInputChannelEvents(t *testing.T) {
	i := newInput(t.Context(), "Label")
	input := make(chan inputIncoming, 2)
	input <- inputChanged{Text: "remote"}
	input <- inputConfirmed{}
	i.input = input
	var out bytes.Buffer
	i.out = &out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      bytes.NewBuffer(nil),
			out:     out,
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	got, err := i.run(i.ctx)
	assert.NoError(t, err)
	assert.Equal(t, "remote", got)
	assert.Equal(t, "remote", i.typed)
	assert.Equal(t, 6, i.cursor)
	assert.Equal(t, 2, len(events))
	_, ok := events[0].(inputInit)
	assert.True(t, ok)
	completed, ok := events[1].(inputComplete)
	assert.True(t, ok)
	assert.Equal(t, "remote", completed.Value)
}

func TestInputRunWithNonEmptyIgnoresEmptyConfirmation(t *testing.T) {
	i := newInput(t.Context(), "Label")
	input := make(chan inputIncoming, 3)
	input <- inputConfirmed{}
	input <- inputChanged{Text: "remote"}
	input <- inputConfirmed{}
	i.input = input
	assert.NoError(t, WithNonEmpty()(i))
	var out bytes.Buffer
	i.out = &out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      bytes.NewBuffer(nil),
			out:     out,
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	got, err := i.run(i.ctx)
	assert.NoError(t, err)
	assert.Equal(t, "remote", got)
	assert.Equal(t, 2, len(events))
	_, ok := events[0].(inputInit)
	assert.True(t, ok)
	completed, ok := events[1].(inputComplete)
	assert.True(t, ok)
	assert.Equal(t, "remote", completed.Value)
}

// regression: readEvents goroutine must not exit on pasteTextError
func TestInputRunPasteFollowedByEnter(t *testing.T) {
	i := newInput(t.Context(), "Label")
	i.in = &chunkReader{
		chunks: [][]byte{
			{'h', 'i'},
			{keyEnter},
		},
	}
	var out bytes.Buffer
	i.out = &out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	got, err := i.run(i.ctx)
	assert.NoError(t, err)
	assert.Equal(t, "hi", got)
}

// regression: multiple paste chunks interleaved with single keys
func TestInputRunTypeAndPaste(t *testing.T) {
	i := newInput(t.Context(), "Label")
	i.in = &chunkReader{
		chunks: [][]byte{
			{'h'},
			{'e', 'l'},
			{'l', 'o'},
			{keyEnter},
		},
	}
	var out bytes.Buffer
	i.out = &out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	got, err := i.run(i.ctx)
	assert.NoError(t, err)
	assert.Equal(t, "hello", got)
}

func TestInputClearError(t *testing.T) {
	i := &input{}
	tio := &termIO{out: &failingBuffer{}}
	err := i.clear(tio)
	assert.Error(t, err)
}

func TestInputCursorActions(t *testing.T) {
	i := &input{
		typed:  "go",
		cursor: 2,
	}
	pressBackspace(i)
	assert.Equal(t, "g", i.typed)
	assert.Equal(t, 1, i.cursor)

	i.pressLeft()
	assert.Equal(t, 0, i.cursor)
	i.pressRight()
	assert.Equal(t, 1, i.cursor)

	i.pressAny('a')
	assert.Equal(t, "ga", i.typed)
}

func TestInputCursorActionsMultiByte(t *testing.T) {
	i := &input{
		typed:  "\u65e5\u672c\u8a9e",
		cursor: 3,
	}
	i.pressLeft()
	pressBackspace(i)
	assert.Equal(t, "\u65e5\u8a9e", i.typed)
	assert.Equal(t, 1, i.cursor)

	i.pressAny('é')
	assert.Equal(t, "\u65e5é\u8a9e", i.typed)
	assert.Equal(t, 2, i.cursor)
	i.pressRight()
	i.pressRight()
	assert.Equal(t, 3, i.cursor)
}

func TestInputPasteMultiByte(t *testing.T) {
	i := &input{typed: "a", cursor: 1}
	paste := []byte("\u65e5\u672cé")
	done, err := i.handleKeyEvent(t.Context(), keyEvent{
		err:   &pasteTextError{buf: paste},
		paste: paste,
	}, true)
	assert.NoError(t, err)
	assert.True(t, !done)
	assert.Equal(t, "a\u65e5\u672cé", i.typed)
	assert.Equal(t, 4, i.cursor)
}

// regression: multi-byte runes must be typed as runes, not as their bytes
func TestInputTypesMultiByteFromPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := startChanIO(ctx, 40, 6)
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	// left three times, backspace over "é", then a Cyrillic rune
	_, err = w.WriteString("héllo\x1b[D\x1b[D\x1b[D\x7fж\r")
	assert.NoError(t, err)
	assert.NoError(t, w.Close())
	got, err := Input(t.Context(), "Label", WithInput(&fdByteReader{f: r}), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, "hжllo", got)
}

func TestInputReadsFromPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := startChanIO(ctx, 40, 6)
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	_, err = w.WriteString("hi\r")
	assert.NoError(t, err)
	assert.NoError(t, w.Close())
	got, err := Input(t.Context(), "Label", WithInput(&fdByteReader{f: r}), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, "hi", got)
}

func TestInputReadsMultipleLeftArrowsFromPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := startChanIO(ctx, 40, 6)
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	_, err = w.Write([]byte{
		0x1b, 0x5b, 0x44,
		0x1b, 0x5b, 0x44,
		0x1b, 0x5b, 0x44,
		'q', 'q', 'q', '\r',
	})
	assert.NoError(t, err)
	assert.NoError(t, w.Close())
	got, err := Input(t.Context(), "Label", WithDefault("apple"), WithInput(&fdByteReader{f: r}), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, "apqqqple", got)
}

func TestInputIgnoresSpecialKeysFromPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := startChanIO(ctx, 40, 6)
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	_, err = w.WriteString("a\x1b[3~b\x1b[Hc\x1bxd\x1b\x1bOPe\r")
	assert.NoError(t, err)
	assert.NoError(t, w.Close())
	got, err := Input(t.Context(), "Label", WithInput(&fdByteReader{f: r}), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, "abcde", got)
}

func TestPasswordReadsFromPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := startChanIO(ctx, 40, 6)
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	_, err = w.WriteString("secret\r")
	assert.NoError(t, err)
	assert.NoError(t, w.Close())
	got, err := Password(t.Context(), "Label", WithInput(&fdByteReader{f: r}), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, "secret", got)
}

func TestInputThenPasswordWithPasteDoesNotLoseBytes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := startChanIO(ctx, 200, 8)
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	t.Cleanup(func() {
		if err := w.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Errorf("close pipe writer: %v", err)
		}
	})
	// step 1: feed login, run Input.
	_, err = w.WriteString("alice\r")
	assert.NoError(t, err)
	gotLogin, err := Input(t.Context(), "Login", WithInput(r), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, "alice", gotLogin)
	// step 2: only after Input has returned, feed the password paste.
	// pre-fix, the Input's reader goroutine is still parked in [os.Stdin.Read]
	// here and grabs the first 16-byte chunk before the new goroutine spins up.
	wantPassword := "0123456789abcdefghijklmnopqrstuv"
	_, err = w.WriteString(wantPassword + "\r")
	assert.NoError(t, err)
	gotPassword, err := Password(t.Context(), "Password", WithInput(r), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, wantPassword, gotPassword)
}

func TestInputRunContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	i := newInput(t.Context(), "Label")
	i.ctx = ctx
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      bytes.NewBuffer(nil),
			out:     &bytes.Buffer{},
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	_, err := i.run(i.ctx)
	assert.Error(t, err)
}

// TestInputRunCancelledReadKeepsNextKey verifies cancellation preserves file input.
func TestInputRunCancelledReadKeepsNextKey(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	mk := func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	i := newInput(t.Context(), "first")
	i.ctx, i.in, i.out, i.makeTermIO = ctx, pr, &bytes.Buffer{}, mk
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		time.Sleep(50 * time.Millisecond)
		_, _ = pw.Write([]byte{'x'})      //nolint:errcheck // test cleanup
		_, _ = pw.Write([]byte{keyEnter}) //nolint:errcheck // test cleanup
	}()
	_, err = i.run(i.ctx)
	assert.Error(t, err)

	j := newInput(t.Context(), "second")
	j.in, j.out, j.makeTermIO = pr, &bytes.Buffer{}, mk
	got, err := j.run(j.ctx)
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
}

// notifyingReader exposes a descriptor while reporting when a blocking read starts.
type notifyingReader struct {
	*os.File
	started chan struct{}
	once    sync.Once
}

// Read signals entry before reading one byte, like a wrapper that limits chunks.
func (r *notifyingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.File.Read(p[:1])
}

// TestInputRunCancelledWrappedReadKeepsNextKey verifies handoff of a blocked read across fresh termIO instances.
func TestInputRunCancelledWrappedReadKeepsNextKey(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	in := &notifyingReader{File: pr, started: make(chan struct{})}
	mk := func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			input:   sharedInputState(in),
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	first := newInput(t.Context(), "first")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first.ctx, first.in, first.out, first.makeTermIO = ctx, in, &bytes.Buffer{}, mk
	finished := make(chan error, 1)
	go func() {
		_, err := first.run(first.ctx)
		finished <- err
	}()
	select {
	case <-in.started:
	case <-time.After(time.Second):
		t.Fatal("first prompt did not start reading")
	}
	cancel()
	select {
	case err := <-finished:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled prompt did not return")
	}
	// Write before starting another prompt: the result must survive without a consumer.
	_, err = pw.Write([]byte{'x', keyEnter})
	assert.NoError(t, err)
	second := newInput(t.Context(), "second")
	second.ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	second.in, second.out, second.makeTermIO = in, &bytes.Buffer{}, mk
	got, err := second.run(second.ctx)
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
}

// TestInputRunBufferedWrappedReader ensures buffered input does not wait on its fd.
func TestInputRunBufferedWrappedReader(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	in := &mockDescriptor{Reader: bytes.NewBufferString("x\r"), fd: pr.Fd()}
	prompt := newInput(t.Context(), "buffered")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	prompt.ctx, prompt.in, prompt.out = ctx, in, &bytes.Buffer{}
	prompt.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	got, err := prompt.run(prompt.ctx)
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
}

func pasteEvent(text string) keyEvent {
	return keyEvent{err: &pasteTextError{buf: []byte(text)}, paste: []byte(text)}
}

// regression: keys returned by one read keep their meaning
func TestInputHandleBufferedKeys(t *testing.T) {
	i := &input{typed: "x", cursor: 1}
	done, err := i.handleKeyEvent(t.Context(), pasteEvent("ab\x7f\x1b[Dc\rd"), true)
	assert.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, "xca", i.typed)
}

func runInputOn(t *testing.T, in io.Reader, option ...opt) (string, error) {
	t.Helper()
	i := newInput(t.Context(), "Label")
	i.in, i.out = in, &bytes.Buffer{}
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	i.ctx = ctx
	return i.read(t.Context(), option...)
}

// regression: a, b, Backspace and Enter typed together must submit "a"
func TestInputRunBufferedKeystrokes(t *testing.T) {
	got, err := runInputOn(t, &chunkReader{chunks: [][]byte{[]byte("ab\x7f\r")}})
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
}

func TestInputRunBufferedKeysInTheMiddle(t *testing.T) {
	in := &chunkReader{chunks: [][]byte{[]byte("\x1b[Da\x1b[Cé\x1b[D\x1b[D"), []byte("é\r")}}
	got, err := runInputOn(t, in, WithDefault("\u65e5\u672c"))
	assert.NoError(t, err)
	assert.Equal(t, "\u65e5aé\u672cé", got)
}

// regression: keys after a confirming Enter stay for the next prompt
func TestInputRunKeepsKeysAfterEnter(t *testing.T) {
	in := &chunkReader{chunks: [][]byte{[]byte("a\rbc\r")}}
	got, err := runInputOn(t, in)
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
	got, err = runInputOn(t, in)
	assert.NoError(t, err)
	assert.Equal(t, "bc", got)
}

func TestInputRendersPastedControlsSafely(t *testing.T) {
	literalInputs(t)
	i := &input{
		Label:  "L",
		typed:  "a\rb\x1b[2J\x7f\u009b",
		cursor: 1,
	}
	var out bytes.Buffer
	tt := &termIO{out: &out}
	bindInput(t, i, tt)
	err := i.render(tt, &bytes.Buffer{})
	assert.NoError(t, err)
	assert.Equal(t, "\rL a␍b␛[2J␡�\x1b[K\x1b[8D", out.String())
}

func TestInputShowAnswerRendersControlsSafely(t *testing.T) {
	var out bytes.Buffer
	literalInputs(t)
	i := &input{Label: "L", typed: "a\rb"}
	i.out = &out
	bindInput(t, i, &termIO{out: &out})
	assert.NoError(t, i.showAnswer())
	assert.Equal(t, "a␍b\n", out.String())
}

// regression: a backspace that deletes nothing must not move the cursor
func TestInputBackspaceAtStartKeepsCursor(t *testing.T) {
	i := &input{typed: "abc", cursor: 0}
	done, err := i.handleKeyEvent(t.Context(), keyEvent{key: 0x7f}, true)
	assert.NoError(t, err)
	assert.True(t, !done)
	assert.Equal(t, "abc", i.typed)
	assert.Equal(t, 0, i.cursor)
}

func TestInputChangedWithCursor(t *testing.T) {
	i := &input{typed: "abc", cursor: 3}
	cursor := 1
	i.applyInputEvent(inputChanged{Text: "abcd", Cursor: &cursor})
	assert.Equal(t, 1, i.cursor)
	cursor = 10
	i.applyInputEvent(inputChanged{Text: "ab", Cursor: &cursor})
	assert.Equal(t, 2, i.cursor)
	// a text-only event derives the cursor from the change in length
	i.applyInputEvent(inputChanged{Text: "abx"})
	assert.Equal(t, 3, i.cursor)
}

// renderInput renders with termWidth content columns plus one spare, or unknown width when zero.
func renderInput(t *testing.T, i *input, termWidth int) string {
	t.Helper()
	literalInputs(t)
	var out bytes.Buffer
	tt := &termIO{out: &out}
	if termWidth > 0 { // zero is an unknown width
		tt.Width = termWidth + 1
	}
	bindInput(t, i, tt)
	err := i.render(tt, &bytes.Buffer{})
	assert.NoError(t, err)
	return out.String()
}

// regression: wide runes must be clipped by terminal columns, not rune count
func TestInputRenderClipsWideRunesByWidth(t *testing.T) {
	// "L " takes two of six columns, leaving four for two wide runes
	got := renderInput(t, &input{Label: "L", typed: "\u754c\u754c\u754c\u754c", cursor: 4}, 6)
	assert.Equal(t, "\rL \u754c\u754c\x1b[K", got)
	got = renderInput(t, &input{Label: "L", typed: "\u754c\u754c\u754c\u754c", cursor: 0}, 6)
	assert.Equal(t, "\rL \u754c\u754c\x1b[K\x1b[4D", got)
	got = renderInput(t, &input{Label: "L", typed: "a\u754c\u754c\u754c", cursor: 2}, 6)
	assert.Equal(t, "\rL \u754c\u754c\x1b[K\x1b[2D", got)
}

func TestInputRenderMovesOverCombiningRunes(t *testing.T) {
	got := renderInput(t, &input{Label: "L", typed: "e\u0301e\u0301x", cursor: 2}, 0)
	assert.Equal(t, "\rL e\u0301e\u0301x\x1b[K\x1b[2D", got)
}

func TestInputRenderClipsWithoutOrphanCombiningRune(t *testing.T) {
	// the window cannot hold the "e" under the combining acute accent
	got := renderInput(t, &input{Label: "L", typed: "ae\u0301bcd", cursor: 6}, 5)
	assert.Equal(t, "\rL bcd\x1b[K", got)
}

// regression: a combining mark at the cursor must not render without its base,
// a cursor inside of a cluster stands on the whole cluster.
func TestInputRenderClipsOrphanCombiningRuneAtCursor(t *testing.T) {
	i := &input{typed: "は\u3099ab", cursor: 1}
	start, end := i.visibleWindow(text(i.typed).clusters(), 2)
	assert.Equal(t, 0, start)
	assert.Equal(t, 1, end)
	got := renderInput(t, &input{Label: "L", typed: "は\u3099ab", cursor: 1}, 4)
	assert.Equal(t, "\rL は\u3099\x1b[K\x1b[2D", got)
}

func TestInputRenderMasksPasswordByRune(t *testing.T) {
	got := renderInput(t, &input{Label: "L", typed: "\u754ce\u0301", cursor: 1, Password: true}, 0)
	assert.Equal(t, "\rL ***\x1b[K\x1b[2D", got)
}

func TestInputRenderLabelFillsRow(t *testing.T) {
	// the label is clipped to keep one value column, which shows the caret's cluster
	got := renderInput(t, &input{Label: "Label", typed: "abc", cursor: 1}, 6)
	assert.Equal(t, "\rLabe…b\x1b[K\x1b[1D", got)
}

// blockingReader reports the start of each read and returns chunks on demand.
type blockingReader struct {
	started chan struct{}
	chunks  chan []byte
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.started <- struct{}{}
	chunk, ok := <-r.chunks
	if !ok {
		return 0, io.EOF
	}
	return copy(p, chunk), nil
}

// regression: a resize must not authorize a read beyond the confirming Enter
func TestInputRunResizeDoesNotReadAhead(t *testing.T) {
	in := &blockingReader{started: make(chan struct{}, 4), chunks: make(chan []byte)}
	resized := make(chan struct{})
	i := newInput(t.Context(), "Label")
	i.in, i.out = in, &bytes.Buffer{}
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:       in,
			out:      out,
			Width:    20,
			Height:   2,
			onResize: resized,
			Restore:  func() error { return nil },
		}, nil
	}
	secondRead := make(chan bool, 1)
	// confirmation waits for a read started after Enter
	i.rules = append(i.rules, func(context.Context, string) error {
		select {
		case <-in.started:
			secondRead <- true
		case <-time.After(200 * time.Millisecond):
			secondRead <- false
		}
		return nil
	})
	finished := make(chan error, 1)
	go func() {
		_, err := i.run(i.ctx)
		finished <- err
	}()
	select {
	case <-in.started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start reading")
	}
	resized <- struct{}{}
	in.chunks <- []byte{keyEnter}
	assert.True(t, !<-secondRead)
	close(in.chunks)
	select {
	case err := <-finished:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("prompt did not return")
	}
}

func TestInputCursorMovesByCluster(t *testing.T) {
	i := &input{typed: "aé👨‍👩b", cursor: 0}
	i.pressRight()
	assert.Equal(t, 1, i.cursor)
	i.pressRight() // over e and its accent
	assert.Equal(t, 3, i.cursor)
	i.pressRight() // over the whole ZWJ sequence
	assert.Equal(t, 6, i.cursor)
	i.pressLeft()
	assert.Equal(t, 3, i.cursor)
	pressBackspace(i) // deletes e with its accent
	assert.Equal(t, "a👨‍👩b", i.typed)
	assert.Equal(t, 1, i.cursor)
}

// pressBackspace applies a backspace the way a key event does.
func pressBackspace(i *input) {
	i.applyInputEvent(i.decodeInputEvent(0x7f))
}

func TestInputBackspaceByCluster(t *testing.T) {
	for _, tt := range []struct {
		name, typed string
		cursor      int
		password    bool
		want        string
		wantCursor  int
	}{
		{"combining mark", "ae\u0301b", 3, false, "ab", 1},
		{"flag", "a🇺🇦", 3, false, "a", 1},
		{"skin tone", "👍🏽x", 2, false, "x", 0},
		{"ZWJ sequence", "a👨\u200d👩\u200d👧", 6, false, "a", 1},
		{"keycap", "1\ufe0f\u20e3", 3, false, "", 0},
		{"at the start", "e\u0301", 0, false, "e\u0301", 0},
		{"beyond the end", "ae\u0301", 9, false, "a", 1},
		{"masked by rune", "ae\u0301", 3, true, "ae", 2},
		{"masked flag by rune", "🇺🇦", 2, true, "🇺", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			i := &input{typed: tt.typed, cursor: tt.cursor, Password: tt.password}
			pressBackspace(i)
			assert.Equal(t, tt.want, i.typed)
			assert.Equal(t, tt.wantCursor, i.cursor)
		})
	}
}

func FuzzInputWindow(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed, uint8(3), uint8(5))
		f.Add(seed, uint8(0), uint8(2))
	}
	f.Fuzz(func(t *testing.T, s string, cursor, avail uint8) {
		i := &input{typed: s, cursor: int(cursor)}
		runes := i.displayRunes()
		cells := text(string(runes)).clusters()
		first, last := i.visibleWindow(cells, int(avail))
		if first < 0 || first > last || last > len(cells) {
			t.Fatalf("window [%d,%d) of %d clusters", first, last, len(cells))
		}
		visible := string(runes[cells.runeOffset(first):cells.runeOffset(last)])
		if avail > 0 && text(visible).width() > int(avail) {
			t.Fatalf("window %q wider than %d", visible, avail)
		}
		for range 3 {
			i.pressLeft()
			i.pressRight()
		}
		pressBackspace(i)
		if !utf8.ValidString(i.typed) && utf8.ValidString(s) {
			t.Fatalf("backspace broke UTF-8: %q", i.typed)
		}
	})
}

// runValidated drives validation with scripted keys and captures output on a 40x3 terminal.
func runValidated(t *testing.T, password bool, keys string, o ...opt) (string, string, error) {
	t.Helper()
	i := newInput(t.Context(), "Label")
	i.Password = password
	var chunks [][]byte
	for _, k := range keys {
		chunks = append(chunks, []byte(string(k)))
	}
	i.in = &chunkReader{chunks: chunks}
	var out bytes.Buffer
	i.out = &out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 40, Height: 3, Restore: func() error { return nil }}, nil
	}
	if err := opts(o).Apply(i); err != nil {
		return "", "", err
	}
	got, err := i.run(i.ctx)
	return got, out.String(), err
}

func TestInputValidatorRejectsThenAccepts(t *testing.T) {
	var seen []string
	check := WithValidation(func(_ context.Context, s string) error {
		seen = append(seen, s)
		if len(s) < 2 {
			return errors.New("too short")
		}
		return nil
	})
	got, out, err := runValidated(t, false, "a\rb\r", check)
	assert.NoError(t, err)
	assert.Equal(t, "ab", got)
	assert.Equal(t, []string{"a", "ab"}, seen)
	assert.Contains(t, out, "✖ ")
	assert.Contains(t, out, "too short")
	assert.Contains(t, out, "\x1b[1A")
}

func TestInputValidatorEOFAfterRejectionReturnsNoValue(t *testing.T) {
	reject := WithValidation(func(context.Context, string) error { return errors.New("no") })
	got, _, err := runValidated(t, false, "a\r", reject)
	assert.Equal(t, "", got)
	assert.ErrorIs(t, err, io.EOF)
}

func TestInputValidatorsRunInOrderAndStopAtFirstFailure(t *testing.T) {
	var calls []string
	first := WithValidation(
		func(context.Context, string) error { calls = append(calls, "first"); return errors.New("one") },
	)
	second := WithValidation(
		func(context.Context, string) error { calls = append(calls, "second"); return errors.New("two") },
	)
	_, out, err := runValidated(t, false, "a\r", first, second)
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, []string{"first"}, calls)
	assert.Contains(t, out, "one")
	assert.True(t, !strings.Contains(out, "two"))
}

func TestInputValidatorEmptyMessageFallsBack(t *testing.T) {
	blank := WithValidation(func(context.Context, string) error { return errors.New("") })
	_, out, err := runValidated(t, false, "a\r", blank)
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "Invalid value")
}

func TestInputValidatorNilIsInvalidOption(t *testing.T) {
	err := WithValidation(nil)(newInput(t.Context(), "x"))
	assert.ErrorIs(t, err, ErrInvalidOption)
}

func TestInputValidatorWrongWidget(t *testing.T) {
	err := WithValidation(func(context.Context, string) error { return nil })(newDropdown(t.Context()))
	assert.ErrorIs(t, err, ErrWrongWidget)
}

func TestInputValidatesDefault(t *testing.T) {
	_, out, err := runValidated(t, false, "\r", WithDefault("bad"), WithRegex(`[0-9]+`, "digits only"))
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "✖ ")
	assert.Contains(t, out, "digits only")
	got, _, err := runValidated(t, false, "\r", WithDefault("42"), WithRegex(`[0-9]+`, "digits only"))
	assert.NoError(t, err)
	assert.Equal(t, "42", got)
}

func TestInputNonEmptyRejectsBlank(t *testing.T) {
	got, out, err := runValidated(t, false, " \r\x7fa\r", WithNonEmpty())
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
	assert.Contains(t, out, "a value is required")
}

func TestInputRegexMatchesWholeString(t *testing.T) {
	re := WithRegex(`a|b`, "a or b")
	got, _, err := runValidated(t, false, "ab\x7fb\x7f\x7fa\r", re)
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
	_, out, err := runValidated(t, false, "ab\r", re)
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "a or b")
	// matches empty without a separate non-empty rule
	got, _, err = runValidated(t, false, "\r", WithRegex(`a*`, "a's"))
	assert.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestInputRegexMalformedOrBlankMessage(t *testing.T) {
	assert.ErrorIs(t, WithRegex(`(`, "m")(newInput(t.Context(), "x")), ErrInvalidOption)
	assert.ErrorIs(t, WithRegex(`a`, "  ")(newInput(t.Context(), "x")), ErrInvalidOption)
}

func TestInputRejectionClearsOnEditAndKeepsText(t *testing.T) {
	_, out, err := runValidated(
		t,
		false,
		"ab\rc",
		WithValidation(func(context.Context, string) error { return errors.New("nope") }),
	)
	assert.ErrorIs(t, err, io.EOF)
	// the stale row is erased by the next frame
	assert.Contains(t, out, "\n\r\x1b[K\x1b[1A\x1b[")
	assert.Contains(t, out, "abc")
}

func TestInputFeedbackSanitizesMessage(t *testing.T) {
	bad := errors.New("line1\nline2\x1b[31m\rtail")
	_, out, err := runValidated(t, false, "a\r", WithValidation(func(context.Context, string) error { return bad }))
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "line1 line2[31m tail")
	assert.True(t, !strings.Contains(out, "\x1b[31mtail"))
}

func TestInputFeedbackClippedToOneRow(t *testing.T) {
	th := NewTheme()
	th.Templates.Input.Feedback = `{{ .Message }}`
	useTheme(t, th)
	_, out, err := runValidated(t, false, "a\r",
		WithValidation(func(context.Context, string) error { return errors.New(strings.Repeat("é", 60)) }))
	assert.ErrorIs(t, err, io.EOF)
	// 40 columns less the guide and the autowrap column
	assert.Contains(t, out, strings.Repeat("é", 36)+"…")
	assert.True(t, !strings.Contains(out, strings.Repeat("é", 37)))
}

func TestInputFeedbackTemplateIsPlain(t *testing.T) {
	th := NewTheme()
	th.Templates.Input.Feedback = `!! {{ .Message }}`
	useTheme(t, th)
	_, out, err := runValidated(t, false, "a\r",
		WithValidation(func(context.Context, string) error { return errors.New("bad") }))
	assert.ErrorIs(t, err, io.EOF)
	assert.Contains(t, out, "!! bad")
	assert.True(t, !strings.Contains(out, "\x1b[31m"))
}

func TestPasswordRejectionStaysMasked(t *testing.T) {
	_, out, err := runValidated(
		t,
		true,
		"secret\r",
		WithValidation(func(context.Context, string) error { return errors.New("too weak") }),
	)
	assert.ErrorIs(t, err, io.EOF)
	// a custom message may repeat the secret, so a password shows a fixed one
	assert.Contains(t, out, "✖ Invalid value")
	assert.True(t, !strings.Contains(out, "too weak"))
	assert.True(t, !strings.Contains(out, "secret"))
}

func TestInputNeedsTwoRowsForFeedback(t *testing.T) {
	i := newInput(t.Context(), "Label")
	i.in = &chunkReader{chunks: [][]byte{{'a'}, {keyEnter}}}
	i.out = &bytes.Buffer{}
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 1, Restore: func() error { return nil }}, nil
	}
	assert.NoError(t, WithValidation(func(context.Context, string) error { return errors.New("no") })(i))
	_, err := i.run(i.ctx)
	assert.ErrorIs(t, err, ErrNoSpace)
}

func TestInputDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()

	got, err := Input(ctx, "Name", io, WithDefault("bob"))
	assert.NoError(t, err)
	assert.Equal(t, "bob", got)

	got, err = Input(ctx, "Name", io, WithDefault(""))
	assert.NoError(t, err)
	assert.Equal(t, "", got)

	got, err = Input(ctx, "Name", io, WithDefault(0))
	assert.NoError(t, err)
	assert.Equal(t, "0", got)

	got, err = Input(ctx, "Name", io, WithDefault(nil))
	assert.NoError(t, err)
	assert.Equal(t, "<nil>", got)

	got, err = Input(ctx, "Name", io, WithDefault("first"), WithDefault("last"))
	assert.NoError(t, err)
	assert.Equal(t, "last", got)

	_, err = Input(ctx, "Name", io)
	assert.ErrorIs(t, err, ErrNoTTY)
}

func TestInputDefaultValidation(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()

	_, err := Input(ctx, "Name", io, WithNonEmpty(), WithDefault(""))
	assert.ErrorIs(t, err, ErrInvalidOption)

	cause := errors.New("nope")
	_, err = Input(ctx, "Name", io, WithDefault("x"),
		WithValidation(func(context.Context, string) error { return cause }))
	assert.ErrorIs(t, err, ErrInvalidOption)
	assert.ErrorIs(t, err, cause)

	_, err = Input(ctx, "Name", io, WithDefault("abc"), WithRegex(`\d+`, "digits"))
	assert.ErrorIs(t, err, ErrInvalidOption)

	got, err := Input(ctx, "Name", io, WithDefault("123"), WithRegex(`\d+`, "digits"))
	assert.NoError(t, err)
	assert.Equal(t, "123", got)

	cctx, cancel := context.WithCancel(ctx)
	_, err = Input(cctx, "Name", io, WithDefault("x"),
		WithValidation(func(context.Context, string) error { cancel(); return nil }))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPasswordDefaultKeepsSecretOut(t *testing.T) {
	io := quiet(t)
	got, err := Password(t.Context(), "Secret", io, WithDefault("hunter2"))
	assert.NoError(t, err)
	assert.Equal(t, "hunter2", got)

	_, err = Password(t.Context(), "Secret", io, WithDefault("hunter2"),
		WithValidation(func(context.Context, string) error { return errors.New("too weak") }))
	assert.ErrorIs(t, err, ErrInvalidOption)
	assert.True(t, !strings.Contains(err.Error(), "hunter2"))
}

// useTheme installs th and routes migrated widgets through it for the test.
func useTheme(t *testing.T, th Theme) {
	t.Helper()
	keepTheme(t)
	assert.NoError(t, defaultIO.installTheme(th))
}

// asciiTheme is the preset theme with ASCII symbols, Guided or Inline.
func asciiTheme(inline bool) Theme {
	th := NewTheme()
	th.Symbols = asciiSymbols
	th.Inline = inline
	return th
}

// themedInput is an input rendering through th on a scripted terminal of the
// given size. Each key is one read. restored reports a released terminal.
func themedInput(
	t *testing.T,
	th Theme,
	width, height int,
	keys ...string,
) (i *input, out *bytes.Buffer, restored *bool) {
	t.Helper()
	useTheme(t, th)
	i = newInput(t.Context(), "Label")
	var chunks [][]byte
	for _, k := range keys {
		chunks = append(chunks, []byte(k))
	}
	out, restored = &bytes.Buffer{}, new(bool)
	i.in, i.out = &chunkReader{chunks: chunks}, out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		restore := func() error { *restored = true; return nil }
		return &termIO{in: in, out: out, Width: width, Height: height, Restore: restore}, nil
	}
	return i, out, restored
}

func TestThemedInputGuidedFrameAnswerAndClear(t *testing.T) {
	i, out, restored := themedInput(t, asciiTheme(false), 20, 5, "a", "\r")
	got, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
	assert.True(t, *restored)
	assert.Equal(t, ""+
		// the label has its own row, the caret stays on the guided value row
		"\r? Label\x1b[K\n\r| \x1b[K"+
		// the next frame starts from the label row
		"\x1b[1A\r? Label\x1b[K\n\r| a\x1b[K"+
		// from the caret row below the frame, then up over both rows
		"\n\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r"+
		"+ Label · a\n", out.String())
}

func TestThemedInputInlineKeepsLabelOnValueRow(t *testing.T) {
	i, out, _ := themedInput(t, asciiTheme(true), 20, 5, "a", "\x1b[D", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, ""+
		"\r? Label \x1b[K"+
		"\r? Label a\x1b[K"+
		"\r? Label a\x1b[K\x1b[1D"+
		"\n\x1b[1A\r\x1b[K"+
		"+ Label · a\n", out.String())
}

func TestThemedInputInlineReservesCaretColumn(t *testing.T) {
	// "? Label " takes 8 of 12 columns, the last one is the caret's: 3 remain
	i, out, _ := themedInput(t, asciiTheme(true), 12, 5, "abcd", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r? Label bcd\x1b[K\n")
}

func TestThemedInputGuidedCaretAfterMovingLeft(t *testing.T) {
	i, out, _ := themedInput(t, asciiTheme(false), 20, 5, "ab", "\x1b[D", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\x1b[1A\r? Label\x1b[K\n\r| ab\x1b[K\x1b[1D")
}

func TestThemedInputRejectionRowGrowsAndShrinks(t *testing.T) {
	check := WithValidation(func(_ context.Context, s string) error {
		if len(s) < 2 {
			return errors.New("too short")
		}
		return nil
	})
	i, out, _ := themedInput(t, asciiTheme(false), 20, 5, "a", "\r", "b", "\r")
	got, err := i.read(t.Context(), check)
	assert.NoError(t, err)
	assert.Equal(t, "ab", got)
	assert.Equal(t, ""+
		"\r? Label\x1b[K\n\r| \x1b[K"+
		"\x1b[1A\r? Label\x1b[K\n\r| a\x1b[K"+
		// the rejection adds a guided row below; the caret returns to column 4
		"\x1b[1A\r? Label\x1b[K\n\r| a\x1b[K\n\r| ! too short\x1b[K\x1b[1A\x1b[4G"+
		// typing clears it, and the stale row is erased
		"\x1b[1A\r? Label\x1b[K\n\r| ab\x1b[K\n\r\x1b[K\x1b[1A\x1b[5G"+
		"\n\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r"+
		"+ Label · ab\n", out.String())
}

func TestThemedInputClearsRejectionRowsOnError(t *testing.T) {
	reject := WithValidation(func(context.Context, string) error { return errors.New("no") })
	i, out, restored := themedInput(t, asciiTheme(false), 20, 5, "a", "\r")
	_, err := i.read(t.Context(), reject)
	assert.ErrorIs(t, err, io.EOF)
	assert.True(t, *restored)
	// three rows, the caret on the second: down two, then up over all three
	assert.True(t, strings.HasSuffix(out.String(),
		"\x1b[1A\x1b[4G\n\n\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r"))
}

// renderThemedInput binds i to a terminal of the given size and draws one frame.
func renderThemedInput(t *testing.T, i *input, width, height int) (string, error) {
	t.Helper()
	var out bytes.Buffer
	tt := &termIO{out: &out, Width: width, Height: height}
	assert.NoError(t, i.bind(t.Context(), tt))
	err := i.render(tt, &bytes.Buffer{})
	return out.String(), err
}

func TestThemedInputRejectionTruncatesAndRefitsOnResize(t *testing.T) {
	i, _, _ := themedInput(t, asciiTheme(false), 0, 0)
	i.typed, i.cursor, i.rejection = "abc", 3, "much too short"
	got, err := renderThemedInput(t, i, 30, 5)
	assert.NoError(t, err)
	assert.Contains(t, got, "\n\r| ! much too short\x1b[K")
	// the same prompt after a resize: the rejection is clipped to the row
	i.rows, i.caretRow = 0, 0
	var out bytes.Buffer
	assert.NoError(t, i.render(&termIO{out: &out, Width: 10, Height: 5}, &bytes.Buffer{}))
	assert.Equal(t, "\r? Label\x1b[K\n\r| abc\x1b[K\n\r| ! much\u2026\x1b[K\x1b[1A\x1b[6G", out.String())
}

func TestThemedInputShortTerminals(t *testing.T) {
	for _, tc := range []struct {
		name      string
		width     int
		height    int
		rejection string
		want      string
		err       error
	}{
		{"label dropped first", 20, 2, "no", "\r| a\x1b[K\n\r| ! no\x1b[K\x1b[1A\x1b[4G", nil},
		{"one row keeps the value", 20, 1, "", "\r| a\x1b[K", nil},
		{"rejection needs a second row", 20, 1, "no", "", ErrNoSpace},
		{"no value column", 3, 5, "", "", ErrNoSpace},
		{"unknown size is unbounded", 0, 0, "", "\r? Label\x1b[K\n\r| a\x1b[K", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, _, _ := themedInput(t, asciiTheme(false), 0, 0)
			i.typed, i.cursor, i.rejection = "a", 1, tc.rejection
			got, err := renderThemedInput(t, i, tc.width, tc.height)
			if tc.err != nil {
				assert.ErrorIs(t, err, tc.err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestThemedInputShrinkKeepsValueOnScreen(t *testing.T) {
	i, _, _ := themedInput(t, asciiTheme(false), 0, 0)
	i.typed, i.cursor = "a", 1
	got, err := renderThemedInput(t, i, 20, 2)
	assert.NoError(t, err)
	assert.Equal(t, "\r? Label\x1b[K\n\r| a\x1b[K", got)
	// one row left: the label goes, and no newline scrolls the value away
	var out bytes.Buffer
	assert.NoError(t, i.render(&termIO{out: &out, Width: 20, Height: 1}, &bytes.Buffer{}))
	assert.Equal(t, "\x1b[1A\r| a\x1b[K", out.String())
	assert.Equal(t, 1, i.rows)
	assert.Equal(t, 0, i.caretRow)
}

func TestThemedInputWideAndCombiningValue(t *testing.T) {
	i, _, _ := themedInput(t, asciiTheme(false), 0, 0)
	// "| " and the caret column leave five of eight columns: the third wide
	// rune does not fit, and the caret after the cluster moves over two
	i.typed, i.cursor = "e\u0301\u754c\u754c\u754c", 2
	got, err := renderThemedInput(t, i, 8, 5)
	assert.NoError(t, err)
	assert.Equal(t, "\r? Label\x1b[K\n\r| e\u0301\u754c\u754c\x1b[K\x1b[4D", got)
}

func TestThemedInputFamilySlotsRender(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Input.Label = `theme {{ .Label }}`
	th.Templates.Input.Answer = `theme {{ .Value }}`
	th.Templates.Input.Feedback = `theme {{ .Message }}`
	reject := WithValidation(func(_ context.Context, s string) error {
		if s == "a" {
			return errors.New("no")
		}
		return nil
	})

	i, out, _ := themedInput(t, th, 20, 5, "a", "\r", "b", "\r")
	_, err := i.read(t.Context(), reject)
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\rtheme Label\x1b[K\n")
	assert.Contains(t, out.String(), "\n\r| theme no\x1b[K")
	assert.True(t, strings.HasSuffix(out.String(), "theme ab\n"))
}

func TestThemedInputHiddenSlots(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Prompt.Label = Hidden
	th.Templates.Prompt.Answer = Hidden
	i, out, _ := themedInput(t, th, 20, 5, "a", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	// no label row and no retained answer; the value row stays
	assert.Equal(t, "\r| \x1b[K\r| a\x1b[K\n\x1b[1A\r\x1b[K", out.String())
}

func TestThemedInputFamilySlots(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Input.Label = `input`
	th.Templates.Number.Label = `number`
	useTheme(t, th)
	label := func(i *input) string {
		t.Helper()
		assert.NoError(t, i.bind(t.Context(), &termIO{out: &bytes.Buffer{}}))
		return i.label.String()
	}
	assert.Equal(t, "input", label(newInput(t.Context(), "x")))
	password := newInput(t.Context(), "x")
	password.Password = true
	assert.Equal(t, "input", label(password)) // Password inherits Input
	assert.True(t, password.slots().Answer == nil)
	assert.Equal(t, "number", label(newNumber[int](t.Context(), "x").prompt))
}

func TestThemedInputHelperErrorsRestoreTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = map[string]any{"fails": func(string) (string, error) { return "", boom }}

	th.Templates.Input.Label = `{{ fails .Label }}`
	i, out, restored := themedInput(t, th, 20, 5, "a", "\r")
	_, err := i.read(t.Context())
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Templates.Input.Label")
	assert.True(t, *restored)
	assert.Equal(t, "", out.String()) // nothing drawn

	th.Templates.Input.Label = ""
	th.Templates.Input.Feedback = `{{ fails .Message }}`
	reject := WithValidation(func(context.Context, string) error { return errors.New("no") })
	i, out, restored = themedInput(t, th, 20, 5, "a", "\r")
	_, err = i.read(t.Context(), reject)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	// the frame of the rejection is not drawn, the previous one is cleared
	assert.True(t, strings.HasSuffix(out.String(), "| a\x1b[K\n\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r"))

	th.Templates.Input.Feedback = ""
	th.Templates.Input.Answer = `{{ fails .Value }}`
	i, _, restored = themedInput(t, th, 20, 5, "a", "\r")
	_, err = i.read(t.Context())
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
}

func TestThemedInputDefaultWithoutTerminal(t *testing.T) {
	strictOutput(t)
	th := asciiTheme(false)
	th.Templates.Input.Label = `{{ fails .Label }}`
	th.Funcs = map[string]any{"fails": func(string) (string, error) { return "", errors.New("boom") }}
	useTheme(t, th)
	i := newInput(t.Context(), "Label")
	var out bytes.Buffer
	i.out = &out
	i.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) {
		t.Fatal("acquired a terminal")
		return nil, ErrNoTTY
	}
	got, err := i.read(t.Context(), WithDefault("dflt"))
	assert.NoError(t, err)
	assert.Equal(t, "dflt", got)
	assert.Equal(t, "", out.String())
}

func TestThemedInputColorsFollowDestination(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	i, out, _ := themedInput(t, asciiTheme(false), 20, 5, "a", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	accent := string(i.look.roles.accent)
	assert.True(t, strings.HasPrefix(accent, "\x1b[38;2;"))
	assert.Contains(t, out.String(), "\r"+accent+"?\x1b[39m \x1b[1mLabel\x1b[0m\x1b[K\n")
	// the background is unknown, so the value keeps the default and the guide is faint
	assert.Contains(t, out.String(), "\n\r\x1b[2m|\x1b[22m a\x1b[K")

	withEnv(t)
	i, out, _ = themedInput(t, asciiTheme(false), 20, 5, "a", "\r")
	_, err = i.read(t.Context())
	assert.NoError(t, err)
	assert.True(t, !strings.Contains(out.String(), "\x1b[3"))
	assert.True(t, !strings.Contains(out.String(), "\x1b[1m"))
}

func TestThemedInputSymbolsFollowEnvironment(t *testing.T) {
	withEnv(t, "TERM=linux")
	i, out, _ := themedInput(t, NewTheme(), 20, 5, "a", "\r")
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r? Label\x1b[K\n\r| a\x1b[K")

	withEnv(t)
	i, out, _ = themedInput(t, NewTheme(), 20, 5, "a", "\r")
	_, err = i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r◆ Label\x1b[K\n\r│ a\x1b[K")
}

func TestThemedInputCapturesThemeAtConstruction(t *testing.T) {
	i, out, _ := themedInput(t, asciiTheme(false), 20, 5, "a", "\r")
	assert.NoError(t, defaultIO.installTheme(asciiTheme(true)))
	_, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "\r? Label\x1b[K\n\r| a\x1b[K")
}

// themedPassword prepares a themed password prompt with simulated keys and captured output.
func themedPassword(t *testing.T, th Theme, keys ...string) (*input, *bytes.Buffer) {
	t.Helper()
	i, out, _ := themedInput(t, th, 40, 5, keys...)
	i.Password = true
	return i, out
}

func TestThemedPasswordMasksWithThemeSymbol(t *testing.T) {
	th := asciiTheme(false)
	th.Symbols.Mask = "•"
	i, out := themedPassword(t, th, "abc", "\x1b[D", "\r")
	got, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "abc", got)
	assert.Contains(t, out.String(), "\n\r| •••\x1b[K\x1b[1D")
	// a password retains no answer line
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[1A\r"))
	assert.True(t, !strings.Contains(out.String(), "abc"))
}

func TestThemedPasswordMultiRuneMaskKeepsRuneEditing(t *testing.T) {
	th := asciiTheme(false)
	th.Symbols.Mask = "é" // one column, two runes
	// a typed combining sequence is two runes, so two masks; backspace in
	// the middle removes one rune, as without a theme
	i, out := themedPassword(t, th, "aéb", "\x1b[D", "\x7f", "\r")
	got, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "aeb", got)
	assert.Contains(t, out.String(), "| éééé\x1b[K\x1b[1D")
	assert.Contains(t, out.String(), "| ééé\x1b[K\x1b[1D")
}

func TestThemedPasswordMaskClipsToWindow(t *testing.T) {
	th := asciiTheme(true)
	th.Symbols.Mask = "#"
	useTheme(t, th)
	i := newInput(t.Context(), "Label")
	i.Password = true
	i.typed, i.cursor = "secret", 6
	// "? Label " takes 8 of 12 columns, the last one is the caret's: 3 remain
	got, err := renderThemedInput(t, i, 12, 5)
	assert.NoError(t, err)
	assert.Equal(t, "\r? Label ###\x1b[K", got)
}

func TestThemedPasswordRejectionIsSecretIndependent(t *testing.T) {
	leak := WithValidation(func(_ context.Context, s string) error {
		if s == "hunter2" {
			return fmt.Errorf("%q is too weak", s)
		}
		return nil
	})
	var seen []string
	th := asciiTheme(false)
	th.Funcs = map[string]any{"spy": func(s string) string { seen = append(seen, s); return s }}
	th.Templates.Password.Label = `{{ spy .Label }}`
	th.Templates.Password.Feedback = `{{ error (spy .Message) }}`
	for _, tc := range []struct {
		name string
		opts []opt
		want string
	}{
		{"theme slot", nil, "\n\r| Invalid value\x1b[K"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen = nil
			i, out := themedPassword(t, th, "hunter2", "\r", "!", "\r")
			got, err := i.read(t.Context(), append([]opt{leak}, tc.opts...)...)
			assert.NoError(t, err)
			assert.Equal(t, "hunter2!", got)
			assert.Contains(t, out.String(), tc.want)
			assert.True(t, !strings.Contains(out.String(), "hunter2"))
			for _, s := range seen {
				assert.True(t, !strings.Contains(s, "hunter2"))
			}
		})
	}
}

func TestThemedPasswordShowsBuiltInRuleMessages(t *testing.T) {
	i, out := themedPassword(t, asciiTheme(false), "\r", "ab", "\r", "c", "\r")
	got, err := i.read(t.Context(), WithNonEmpty(), WithRegex(`.{3,}`, "at least 3 characters"))
	assert.NoError(t, err)
	assert.Equal(t, "abc", got)
	assert.Contains(t, out.String(), "\n\r| ! a value is required\x1b[K")
	assert.Contains(t, out.String(), "\n\r| ! at least 3 characters\x1b[K")
}

func TestThemedPasswordFailingFeedbackKeepsSecretOut(t *testing.T) {
	th := asciiTheme(false)
	th.Funcs = map[string]any{"fails": func(s string) (string, error) { return "", fmt.Errorf("cannot show %q", s) }}
	th.Templates.Password.Feedback = `{{ fails .Message }}`
	leak := WithValidation(func(_ context.Context, s string) error { return errors.New(s) })
	i, out := themedPassword(t, th, "hunter2", "\r")
	_, err := i.read(t.Context(), leak)
	assert.Contains(t, err.Error(), "theme.Templates.Password.Feedback")
	assert.Contains(t, err.Error(), `cannot show "Invalid value"`)
	assert.True(t, !strings.Contains(err.Error(), "hunter2"))
	assert.True(t, !strings.Contains(out.String(), "hunter2"))
}
