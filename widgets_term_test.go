// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
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
	value, err := Input(context.Background(), "Name",
		WithInput(strings.NewReader("alice\r")), WithOutput(&out))
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
	value, err := Input(context.Background(), "Name", WithInput(strings.NewReader("bob\r")), WithOutput(io.Discard))
	assert.NoError(t, err)
	assert.Equal(t, "bob", value)
}

func TestDropdown_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	// Down, Enter
	value, err := Dropdown(context.Background(), "Pick", []string{"a", "b", "c"},
		WithInput(strings.NewReader("\x1b[B\r")), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "b", value)
}

func TestConfirm_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	ok := Confirm(context.Background(), "Proceed",
		WithInput(strings.NewReader("\r")), WithOutput(&out))
	assert.True(t, ok)
}

func TestRoutingWrapperInputBufferOutput(t *testing.T) {
	terst.StrictOutput(t)
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
	terst.StrictOutput(t)
	cio := terst.StartChanIO(t.Context(), 80, 24)
	got, err := Input(t.Context(), "Name", WithInput(strings.NewReader("bob\r")), WithOutput(cio))
	if errors.Is(err, ErrNoTTY) {
		t.Fatal("managed terminal must stay interactive")
	}
	_ = got
}
