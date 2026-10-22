// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
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

func TestRoutingWrapperInputBufferOutput(t *testing.T) {
	strictOutput(t)
	var out bytes.Buffer
	got, err := Input(t.Context(), "Name",
		WithInput(strings.NewReader("typed\r")), WithOutput(&out), WithDefault("dflt"))
	assert.NoError(t, err)
	assert.Equal(t, "dflt", got)
	assert.Equal(t, "", out.String())

	_, err = Input(t.Context(), "Name", WithInput(strings.NewReader("typed\r")), WithOutput(&out))
	assert.ErrorIs(t, err, ErrNoTTY)

	f, ferr := os.CreateTemp(t.TempDir(), "out")
	assert.NoError(t, ferr)
	defer f.Close() //nolint:errcheck
	_, err = Dropdown(t.Context(), "Pick", []string{"a"}, WithOutput(f), WithInput(f))
	assert.ErrorIs(t, err, ErrNoTTY)
}

func TestInteractiveStillWorksOnManagedOutput(t *testing.T) {
	// production classification: a managed terminal with scripted input
	strictOutput(t)
	cio := startChanIO(t.Context(), 80, 24)
	got, err := Input(t.Context(), "Name", WithInput(strings.NewReader("bob\r")), WithOutput(cio))
	if errors.Is(err, ErrNoTTY) {
		t.Fatal("managed terminal must stay interactive")
	}
	_ = got
}
