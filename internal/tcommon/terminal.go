// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tcommon

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/terst"
)

// TermIOFunc makes a widget terminal on the given streams, as the makeTermIO
// seam of a widget does.
type TermIOFunc func(io.Reader, io.Writer) (*term.IO, error)

// IO is the terminal on empty buffers, for code that takes one directly.
func (f TermIOFunc) IO() *term.IO {
	tio, _ := f(bytes.NewBuffer(nil), &bytes.Buffer{}) //nolint:errcheck // a fixed terminal never fails
	return tio
}

// FixedTermIO builds a widget terminal of a fixed size.
func FixedTermIO(width, height int) TermIOFunc {
	return func(in io.Reader, out io.Writer) (*term.IO, error) {
		return &term.IO{In: in, Out: out, Width: width, Height: height, Restore: func() error { return nil }}, nil
	}
}

// UseFrames makes a terminal without the arbiter the default IO for the test,
// with keys as its keyboard. Every widget write is one chunk of the output,
// exactly as the widget wrote it.
func UseFrames(t *testing.T) (*terst.Keys, *terst.Output) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cio := &term.ChanIO{Ctx: ctx, Out: make(chan string)}
	out := terst.NewOutput(t)
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
	keys := terst.NewKeys(t)
	terst.UseIO(t, keys, cio)
	return keys, out
}
