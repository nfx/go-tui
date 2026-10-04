// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestRenderNormal(t *testing.T) {
	i := &input{
		Label:          "Enter text:",
		typed:          "hello",
		cursor:         5,
		LabelTemplate:  "{{.}} ",
		AnswerTemplate: "{{.Label}}: {{.Answer}}\n",
	}
	err := i.parseTemplates()
	assert.NoError(t, err)

	var out bytes.Buffer
	io := &termIO{out: &out} // Assuming termIO has an out field; adjust if needed

	frame := &bytes.Buffer{}
	err = i.render(io, frame)
	assert.NoError(t, err)

	expected := "\rEnter text: hello\x1b[K"
	assert.Equal(t, expected, out.String())
}

func TestRenderCursorAtStart(t *testing.T) {
	i := &input{
		Label:          "Input:",
		typed:          "abc",
		cursor:         0,
		LabelTemplate:  "{{.}} ",
		AnswerTemplate: "{{.Label}}: {{.Answer}}\n",
	}
	err := i.parseTemplates()
	assert.NoError(t, err)

	var out bytes.Buffer
	io := &termIO{out: &out}

	frame := &bytes.Buffer{}
	err = i.render(io, frame)
	assert.NoError(t, err)

	expected := "\rInput: abc\x1b[K\x1b[3D"
	assert.Equal(t, expected, out.String())
}

func TestRenderWriteError(t *testing.T) {
	i := &input{
		Label:          "Test:",
		typed:          "test",
		cursor:         0,
		LabelTemplate:  "{{.}} ",
		AnswerTemplate: "{{.Label}}: {{.Answer}}\n",
	}
	err := i.parseTemplates()
	assert.NoError(t, err)

	// Mock a failing writer
	failingWriter := &failingBuffer{}
	tio := &termIO{out: failingWriter}

	frame := &bytes.Buffer{}
	err = i.render(tio, frame)
	assert.Error(t, err)
	assert.ErrorIs(t, err, io.EOF)
}

// Helper struct for testing write errors.
type failingBuffer struct{}

func (f *failingBuffer) Write(p []byte) (n int, err error) {
	return 0, io.EOF
}

func TestInputWithDefault(t *testing.T) {
	i := newInput("label")
	err := WithDefault("abc")(i)
	assert.NoError(t, err)
	assert.Equal(t, "abc", i.typed)
	assert.Equal(t, 3, i.cursor)
}

func TestInputWithNonEmpty(t *testing.T) {
	i := newInput("label")
	err := WithNonEmpty()(i)
	assert.NoError(t, err)
	assert.True(t, i.CheckFn != nil)
}

func TestInputShowAnswerVisibility(t *testing.T) {
	i := newInput("Name")
	i.typed = "value"
	err := i.parseTemplates()
	assert.NoError(t, err)

	var out bytes.Buffer
	i.out = &out

	err = i.showAnswer()
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
	_, err := Input("Label", func(any) error { return io.EOF })
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestInputReadRunError(t *testing.T) {
	_, err := Input("Label", inputOpt(func(i *input) error {
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
	err := inputOpt(func(*input) error { return nil })("nope")
	if err == nil {
		t.Fatalf("expected error")
	}
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

func TestInputReadTemplateError(t *testing.T) {
	i := newInput("Label")
	i.LabelTemplate = "{{"
	_, err := i.read()
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
	i := newInput("Label")
	assert.NoError(t, WithNonEmpty()(i))
	tio := &termIO{in: bytes.NewBuffer([]byte{keyEnter})}
	out, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "", out)
	assert.Equal(t, "", i.typed)
}

func TestInputRunEmitsLifecycleEvents(t *testing.T) {
	i := newInput("Label")
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
	assert.NoError(t, i.parseTemplates())
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	got, err := i.run()
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
	assert.Equal(t, 2, len(events))
	_, ok := events[0].(inputInit)
	assert.True(t, ok)
	_, ok = events[1].(inputComplete)
	assert.True(t, ok)
}

func TestInputRunConsumesInputChannelEvents(t *testing.T) {
	i := newInput("Label")
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
	assert.NoError(t, i.parseTemplates())
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	got, err := i.run()
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
	i := newInput("Label")
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
	assert.NoError(t, i.parseTemplates())
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	got, err := i.run()
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
	i := newInput("Label")
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
	assert.NoError(t, i.parseTemplates())
	got, err := i.run()
	assert.NoError(t, err)
	assert.Equal(t, "hi", got)
}

// regression: multiple paste chunks interleaved with single keys
func TestInputRunTypeAndPaste(t *testing.T) {
	i := newInput("Label")
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
	assert.NoError(t, i.parseTemplates())
	got, err := i.run()
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
	i.pressBackspace()
	assert.Equal(t, "g", i.typed)
	assert.Equal(t, 1, i.cursor)

	i.pressLeft()
	assert.Equal(t, 0, i.cursor)
	i.pressRight()
	assert.Equal(t, 1, i.cursor)

	i.pressAny('a')
	assert.Equal(t, "ga", i.typed)
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
	got, err := Input("Label", WithInput(&fdByteReader{f: r}), WithOutput(cio))
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
	got, err := Input("Label", WithDefault("apple"), WithInput(&fdByteReader{f: r}), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, "apqqqple", got)
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
	got, err := Password("Label", WithInput(&fdByteReader{f: r}), WithOutput(cio))
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
	gotLogin, err := Input("Login", WithInput(r), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, "alice", gotLogin)
	// step 2: only after Input has returned, feed the password paste.
	// pre-fix, the Input's reader goroutine is still parked in [os.Stdin.Read]
	// here and grabs the first 16-byte chunk before the new goroutine spins up.
	wantPassword := "0123456789abcdefghijklmnopqrstuv"
	_, err = w.WriteString(wantPassword + "\r")
	assert.NoError(t, err)
	gotPassword, err := Password("Password", WithInput(r), WithOutput(cio))
	assert.NoError(t, err)
	assert.Equal(t, wantPassword, gotPassword)
}

func TestInputRunContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	i := newInput("Label")
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
	assert.NoError(t, i.parseTemplates())
	_, err := i.run()
	assert.Error(t, err)
}

// TestInputRunCancelledReadKeepsNextKey verifies cancellation preserves file input.
func TestInputRunCancelledReadKeepsNextKey(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close()
	defer pw.Close()
	mk := func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	i := newInput("first")
	i.ctx, i.in, i.out, i.makeTermIO = ctx, pr, &bytes.Buffer{}, mk
	assert.NoError(t, i.parseTemplates())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		time.Sleep(50 * time.Millisecond)
		_, _ = pw.Write([]byte{'x'})
		_, _ = pw.Write([]byte{keyEnter})
	}()
	_, err = i.run()
	assert.Error(t, err)

	j := newInput("second")
	j.in, j.out, j.makeTermIO = pr, &bytes.Buffer{}, mk
	assert.NoError(t, j.parseTemplates())
	got, err := j.run()
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

// TestInputRunCancelledWrappedReadKeepsNextKey verifies handoff of a blocked read.
func TestInputRunCancelledWrappedReadKeepsNextKey(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close()
	defer pw.Close()
	in := &notifyingReader{File: pr, started: make(chan struct{})}
	mk := func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	first := newInput("first")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first.ctx, first.in, first.out, first.makeTermIO = ctx, in, &bytes.Buffer{}, mk
	assert.NoError(t, first.parseTemplates())
	finished := make(chan error, 1)
	go func() {
		_, err := first.run()
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
		assert.True(t, errors.Is(err, context.Canceled))
	case <-time.After(time.Second):
		t.Fatal("cancelled prompt did not return")
	}
	// Write before starting another prompt: the result must survive without a consumer.
	_, err = pw.Write([]byte{'x', keyEnter})
	assert.NoError(t, err)
	second := newInput("second")
	second.ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	second.in, second.out, second.makeTermIO = in, &bytes.Buffer{}, mk
	assert.NoError(t, second.parseTemplates())
	got, err := second.run()
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
}

// TestInputRunBufferedWrappedReader ensures buffered input does not wait on its fd.
func TestInputRunBufferedWrappedReader(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close()
	defer pw.Close()
	in := &mockDescriptor{Reader: bytes.NewBufferString("x\r"), fd: pr.Fd()}
	prompt := newInput("buffered")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	prompt.ctx, prompt.in, prompt.out = ctx, in, &bytes.Buffer{}
	prompt.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	assert.NoError(t, prompt.parseTemplates())
	got, err := prompt.run()
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
}
