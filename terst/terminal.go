// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"context"
	"io"
	"testing"

	"github.com/nfx/go-tui/internal/term"
)

// UseTerminal makes an 80×24 terminal the default IO for the test, with keys
// as its keyboard. The returned output is what reaches the screen: the
// terminal composes widgets and log lines as a real one would.
func UseTerminal(t *testing.T) (*Keys, *Output) {
	t.Helper()
	keys := NewKeys(t)
	return keys, UseTerminalInput(t, keyboard{keys})
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
