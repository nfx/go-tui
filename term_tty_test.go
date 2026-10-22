// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestHostsTerminal(t *testing.T) {
	assert.True(t, !hostsTerminal(&bytes.Buffer{}))
	assert.True(t, !hostsTerminal(io.Discard))
	f, err := os.CreateTemp(t.TempDir(), "out")
	assert.NoError(t, err)
	defer f.Close() //nolint:errcheck
	assert.True(t, !hostsTerminal(f))
	assert.True(t, hostsTerminal(startChanIO(t.Context(), 80, 24)))
}

func TestHostsTerminalNotInCI(t *testing.T) {
	prev := termGetSize
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	t.Cleanup(func() { termGetSize = prev })
	tty := &mockDescriptor{Writer: &bytes.Buffer{}, fd: 7}
	assert.True(t, hostsTerminal(tty))
	withEnv(t, "CI=true")
	assert.True(t, !hostsTerminal(tty))
	assert.True(t, hostsTerminal(startChanIO(t.Context(), 80, 24)))
}

func TestOutputIONotInCI(t *testing.T) {
	prev := termGetSize
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	t.Cleanup(func() { termGetSize = prev })
	withEnv(t, "CI=true")
	tty := &mockDescriptor{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}, fd: 7}
	_, err := makeOutputIO(tty, tty)
	assert.ErrorIs(t, err, ErrNoTTY)
}
