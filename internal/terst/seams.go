// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"bytes"
	"io"
	"strings"
	"testing"

	xterm "golang.org/x/term"

	"github.com/nfx/go-tui/internal/term"
)

// WithEnv installs an output environment fixture built from KEY=VALUE pairs.
func WithEnv(t *testing.T, pairs ...string) {
	t.Helper()
	vars := map[string]string{}
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		vars[k] = v
	}
	Swap(t, &term.Default.Env, term.ReadOutputEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok }))
}

func TTYWriter(t *testing.T) *MockDescriptor {
	t.Helper()
	Swap(t, &term.WriterChecker, func(int) bool { return true })
	Swap(t, &term.ConsoleVT, func(io.Writer) bool { return true })
	return &MockDescriptor{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}, FD: 7}
}

// Swap sets *p to v for the test and restores the previous value in cleanup.
func Swap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	prev := *p
	*p = v
	t.Cleanup(func() { *p = prev })
}

// StrictOutput restores production terminal classification for a test, where
// writers without terminal capabilities are not terminals.
func StrictOutput(t *testing.T) {
	t.Helper()
	Swap(t, &term.OutputIsTerminal, term.HostsTerminal)
}

// ProbingKeyboard makes input on fd a terminal that answers background
// queries, without changing any terminal mode.
func ProbingKeyboard(t *testing.T, fd uintptr) {
	t.Helper()
	prevInput := term.InputChecker
	Swap(t, &term.InputChecker, func(f int) bool { return f == int(fd) || prevInput(f) })
	Swap(t, &term.ProbeSupported, true)
	Swap(t, &term.MakeRaw, func(int) (*xterm.State, error) { return &xterm.State{}, nil })
	Swap(t, &term.Restore, func(int, *xterm.State) error { return nil })
}
