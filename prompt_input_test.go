// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/theme"
	"io"
	"os"
	"sync"
	"testing"
	"time"

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
	th := theme.New()
	th.Inline = true
	th.Templates.Input.Label = `{{ .Label }}`
	th.Templates.Input.Answer = `{{ .Value }}`
	useTheme(t, th)
}

func TestInputShowAnswerVisibility(t *testing.T) {
	literalInputs(t)
	i := newInput(t.Context(), "Name")
	i.typed = "value"
	var out bytes.Buffer
	i.Out = &out
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
	_, err := Input(t.Context(), "Label", WithOutput(io.Discard), option.OpT(func(i *input) error {
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
	err := option.OpT(func(*input) error { return nil })("nope")
	if !errors.Is(err, ErrWrongWidget) {
		t.Fatalf("expected ErrWrongWidget, got %v", err)
	}
}

func TestInputRunEmitsLifecycleEvents(t *testing.T) {
	i := newInput(t.Context(), "Label")
	i.In = &chunkReader{
		chunks: [][]byte{
			{'x'},
			{keyEnter},
		},
	}
	var out bytes.Buffer
	i.Out = &out
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
	got, err := i.run(i.Ctx)
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
	i.Out = &out
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
	got, err := i.run(i.Ctx)
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
	i.Out = &out
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
	got, err := i.run(i.Ctx)
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
	i.In = &chunkReader{
		chunks: [][]byte{
			{'h', 'i'},
			{keyEnter},
		},
	}
	var out bytes.Buffer
	i.Out = &out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	got, err := i.run(i.Ctx)
	assert.NoError(t, err)
	assert.Equal(t, "hi", got)
}

// regression: multiple paste chunks interleaved with single keys
func TestInputRunTypeAndPaste(t *testing.T) {
	i := newInput(t.Context(), "Label")
	i.In = &chunkReader{
		chunks: [][]byte{
			{'h'},
			{'e', 'l'},
			{'l', 'o'},
			{keyEnter},
		},
	}
	var out bytes.Buffer
	i.Out = &out
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	got, err := i.run(i.Ctx)
	assert.NoError(t, err)
	assert.Equal(t, "hello", got)
}

func TestInputClearError(t *testing.T) {
	i := &input{}
	tio := &termIO{out: &failingBuffer{}}
	err := i.clear(tio)
	assert.Error(t, err)
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
	i.Ctx = ctx
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      bytes.NewBuffer(nil),
			out:     &bytes.Buffer{},
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	_, err := i.run(i.Ctx)
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
	i.Ctx, i.In, i.Out, i.makeTermIO = ctx, pr, &bytes.Buffer{}, mk
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		time.Sleep(50 * time.Millisecond)
		_, _ = pw.Write([]byte{'x'})      //nolint:errcheck // test cleanup
		_, _ = pw.Write([]byte{keyEnter}) //nolint:errcheck // test cleanup
	}()
	_, err = i.run(i.Ctx)
	assert.Error(t, err)

	j := newInput(t.Context(), "second")
	j.In, j.Out, j.makeTermIO = pr, &bytes.Buffer{}, mk
	got, err := j.run(j.Ctx)
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
	first.Ctx, first.In, first.Out, first.makeTermIO = ctx, in, &bytes.Buffer{}, mk
	finished := make(chan error, 1)
	go func() {
		_, err := first.run(first.Ctx)
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
	second.Ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	second.In, second.Out, second.makeTermIO = in, &bytes.Buffer{}, mk
	got, err := second.run(second.Ctx)
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
	prompt.Ctx, prompt.In, prompt.Out = ctx, in, &bytes.Buffer{}
	prompt.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	got, err := prompt.run(prompt.Ctx)
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

func runInputOn(t *testing.T, in io.Reader, option ...option.Opt) (string, error) {
	t.Helper()
	i := newInput(t.Context(), "Label")
	i.In, i.Out = in, &bytes.Buffer{}
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	i.Ctx = ctx
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

func TestInputShowAnswerRendersControlsSafely(t *testing.T) {
	var out bytes.Buffer
	literalInputs(t)
	i := &input{Label: "L", typed: "a\rb"}
	i.Out = &out
	bindInput(t, i, &termIO{out: &out})
	assert.NoError(t, i.showAnswer())
	assert.Equal(t, "a␍b\n", out.String())
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
	i.In, i.Out = in, &bytes.Buffer{}
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
		_, err := i.run(i.Ctx)
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

func TestInputNeedsTwoRowsForFeedback(t *testing.T) {
	i := newInput(t.Context(), "Label")
	i.In = &chunkReader{chunks: [][]byte{{'a'}, {keyEnter}}}
	i.Out = &bytes.Buffer{}
	i.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 1, Restore: func() error { return nil }}, nil
	}
	assert.NoError(t, WithValidation(func(context.Context, string) error { return errors.New("no") })(i))
	_, err := i.run(i.Ctx)
	assert.ErrorIs(t, err, ErrNoSpace)
}
