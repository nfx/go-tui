// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"bytes"
	"github.com/nfx/go-tui/internal/errs"
	"io"
	"os"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestHostsTerminal(t *testing.T) {
	assert.True(t, !HostsTerminal(&bytes.Buffer{}))
	assert.True(t, !HostsTerminal(io.Discard))
	f, err := os.CreateTemp(t.TempDir(), "out")
	assert.NoError(t, err)
	defer f.Close() //nolint:errcheck
	assert.True(t, !HostsTerminal(f))
	assert.True(t, HostsTerminal(startChanIO(t.Context())))
}

func TestHostsTerminalNotInCI(t *testing.T) {
	prev := GetSize
	GetSize = func(int) (int, int, error) { return 80, 24, nil }
	t.Cleanup(func() { GetSize = prev })
	tty := &mockDescriptor{Writer: &bytes.Buffer{}, fd: 7}
	assert.True(t, HostsTerminal(tty))
	withEnv(t, "CI=true")
	assert.True(t, !HostsTerminal(tty))
	assert.True(t, HostsTerminal(startChanIO(t.Context())))
}

func TestOutputIONotInCI(t *testing.T) {
	prev := GetSize
	GetSize = func(int) (int, int, error) { return 80, 24, nil }
	t.Cleanup(func() { GetSize = prev })
	withEnv(t, "CI=true")
	tty := &mockDescriptor{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}, fd: 7}
	_, err := MakeOutputIO(tty, tty)
	assert.ErrorIs(t, err, errs.ErrNoTTY)
}
