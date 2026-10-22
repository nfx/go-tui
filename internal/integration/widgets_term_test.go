// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"context"
	"errors"
	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/term"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestInput_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	value, err := tui.Input(context.Background(), "Name",
		tui.WithInput(strings.NewReader("alice\r")), tui.WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "alice", value)
	assert.Contains(t, out.String(), "Name")
}

func TestSharedInputState_CancelledClosedReaderReleased(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	in := terst.NewNotifyingReader(pr)
	tio := &term.IO{In: in}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tio.ReadEvents(ctx, nil)
	<-in.Started
	cancel()
	tio.AwaitReader()
	assert.NoError(t, pr.Close())
	<-tio.Input.Read.Done
	assert.Error(t, tio.Input.Read.Event.Err)
	term.InputStates.Lock()
	_, retained := term.InputStates.States[in]
	term.InputStates.Unlock()
	if retained {
		t.Fatal("closed input retained after its cancelled read finished")
	}
}

func TestInput_ScriptedInputDefaultOutput(t *testing.T) {
	value, err := tui.Input(
		context.Background(),
		"Name",
		tui.WithInput(strings.NewReader("bob\r")),
		tui.WithOutput(io.Discard),
	)
	assert.NoError(t, err)
	assert.Equal(t, "bob", value)
}

func TestDropdown_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	// Down, Enter
	value, err := tui.Dropdown(context.Background(), "Pick", []string{"a", "b", "c"},
		tui.WithInput(strings.NewReader("\x1b[B\r")), tui.WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "b", value)
}

func TestConfirm_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	ok := tui.Confirm(context.Background(), "Proceed",
		tui.WithInput(strings.NewReader("\r")), tui.WithOutput(&out))
	assert.True(t, ok)
}

func TestRoutingWrapperInputBufferOutput(t *testing.T) {
	terst.StrictOutput(t)
	var out bytes.Buffer
	got, err := tui.Input(t.Context(), "Name",
		tui.WithInput(strings.NewReader("typed\r")), tui.WithOutput(&out), tui.WithDefault("dflt"))
	assert.NoError(t, err)
	assert.Equal(t, "dflt", got)
	assert.Equal(t, "", out.String())

	_, err = tui.Input(t.Context(), "Name", tui.WithInput(strings.NewReader("typed\r")), tui.WithOutput(&out))
	assert.ErrorIs(t, err, tui.ErrNoTTY)

	f, ferr := os.CreateTemp(t.TempDir(), "out")
	assert.NoError(t, ferr)
	defer f.Close() //nolint:errcheck
	_, err = tui.Dropdown(t.Context(), "Pick", []string{"a"}, tui.WithOutput(f), tui.WithInput(f))
	assert.ErrorIs(t, err, tui.ErrNoTTY)
}

func TestInteractiveStillWorksOnManagedOutput(t *testing.T) {
	// production classification: a managed terminal with scripted input
	terst.StrictOutput(t)
	cio := terst.StartChanIO(t.Context(), 80, 24)
	got, err := tui.Input(t.Context(), "Name", tui.WithInput(strings.NewReader("bob\r")), tui.WithOutput(cio))
	if errors.Is(err, tui.ErrNoTTY) {
		t.Fatal("managed terminal must stay interactive")
	}
	_ = got
}
