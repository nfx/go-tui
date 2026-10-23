// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/nfx/go-tui/internal/term"
)

func NewTermIO(width, height int) *term.IO {
	return &term.IO{
		In:      bytes.NewBuffer(nil),
		Out:     &bytes.Buffer{},
		Width:   width,
		Height:  height,
		Restore: func() error { return nil },
	}
}

// FixedTermIO builds a widget terminal of a fixed size on the given streams,
// for the makeTermIO seam of a widget.
func FixedTermIO(width, height int) func(io.Reader, io.Writer) (*term.IO, error) {
	return func(in io.Reader, out io.Writer) (*term.IO, error) {
		return &term.IO{In: in, Out: out, Width: width, Height: height, Restore: func() error { return nil }}, nil
	}
}

// UseTerminal makes an 80×24 terminal the default IO for the test, with keys
// as its keyboard. The returned output is what reaches the screen: the
// terminal composes widgets and log lines as a real one would.
func UseTerminal(t *testing.T) (*Keys, *Output) {
	t.Helper()
	keys := NewKeys(t)
	return keys, UseTerminalInput(t, &MockDescriptor{Reader: keys})
}

// UseKeys is [UseTerminal] for a test that only types.
func UseKeys(t *testing.T) *Keys {
	t.Helper()
	keys, _ := UseTerminal(t)
	return keys
}

// UseTerminalInput is [UseTerminal] reading from in, such as a pipe. A nil in
// fails the test when it is read.
func UseTerminalInput(t *testing.T, in io.Reader) *Output {
	t.Helper()
	if in == nil {
		in = failReader{t}
	}
	ctx, cancel := context.WithCancel(t.Context())
	out := NewOutput(t)
	cio := term.NewUnstartedIO(ctx, 80, 24, 0)
	go cio.HandleViewports(ctx)
	go cio.ForwardTo(ctx, out)
	t.Cleanup(func() {
		cancel()
		close(cio.In)
		close(cio.Out)
	})
	UseIO(t, in, cio)
	return out
}

// UseFrames makes a terminal without the arbiter the default IO for the test,
// with keys as its keyboard. Every widget write is one chunk of the output,
// exactly as the widget wrote it.
func UseFrames(t *testing.T) (*Keys, *Output) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cio := &term.ChanIO{Ctx: ctx, Out: make(chan string)}
	out := NewOutput(t)
	pumped := make(chan struct{})
	go func() {
		defer close(pumped)
		for {
			select {
			case chunk := <-cio.Out:
				out.Write([]byte(chunk)) //nolint:errcheck // Output never fails
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-pumped
	})
	keys := NewKeys(t)
	UseIO(t, keys, cio)
	return keys, out
}
