// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"context"
	"errors"
	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/tcommon"
	"github.com/nfx/go-tui/internal/term"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/terst"
)

func TestInput_ScriptedReaderAndWriter(t *testing.T) {
	keys, out := terst.UseTerminal(t)
	keys.Type("alice\r")
	value, err := tui.Input(context.Background(), "Name")
	assert.NoError(t, err)
	assert.Equal(t, "alice", value)
	out.AwaitOutput("Name")
}

func TestSharedInputState_CancelledClosedReaderReleased(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	in := tcommon.NewNotifyingReader(pr)
	tio := &term.IO{In: in}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tio.ReadEvents(ctx, nil)
	<-in.Started
	cancel()
	tio.AwaitReader()
	if runtime.GOOS == "windows" {
		// closing a pipe read end blocks on Windows while a Read is in flight,
		// so unblock the pending read with EOF from the write end first.
		assert.NoError(t, pw.Close())
	}
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
	keys := terst.UseKeys(t)
	keys.Type("bob\r")
	value, err := tui.Input(
		context.Background(),
		"Name",
	)
	assert.NoError(t, err)
	assert.Equal(t, "bob", value)
}

func TestDropdown_ScriptedReaderAndWriter(t *testing.T) {
	keys := terst.UseKeys(t)
	keys.Type(terst.KeyDown + terst.KeyEnter)
	value, err := tui.Dropdown(context.Background(), "Pick", []string{"a", "b", "c"})
	assert.NoError(t, err)
	assert.Equal(t, "b", value)
}

func TestConfirm_ScriptedReaderAndWriter(t *testing.T) {
	keys := terst.UseKeys(t)
	keys.Type(terst.KeyEnter)
	ok := tui.Confirm(context.Background(), "Proceed")
	assert.True(t, ok)
}

func TestRoutingWrapperInputBufferOutput(t *testing.T) {
	terst.StrictOutput(t)
	var out bytes.Buffer
	terst.UseIO(t, strings.NewReader("typed\r"), &out)
	got, err := tui.Input(t.Context(), "Name",
		tui.WithDefault("dflt"))
	assert.NoError(t, err)
	assert.Equal(t, "dflt", got)
	assert.Equal(t, "", out.String())

	terst.UseIO(t, strings.NewReader("typed\r"), &out)
	_, err = tui.Input(t.Context(), "Name")
	assert.ErrorIs(t, err, tui.ErrNoTTY)

	f, ferr := os.CreateTemp(t.TempDir(), "out")
	assert.NoError(t, ferr)
	defer f.Close() //nolint:errcheck
	terst.UseIO(t, f, f)
	_, err = tui.Dropdown(t.Context(), "Pick", []string{"a"})
	assert.ErrorIs(t, err, tui.ErrNoTTY)
}

func TestInteractiveStillWorksOnManagedOutput(t *testing.T) {
	// production classification: a managed terminal with scripted input
	terst.StrictOutput(t)
	terst.UseTerminalInput(t, strings.NewReader("bob\r"))
	got, err := tui.Input(t.Context(), "Name")
	if errors.Is(err, tui.ErrNoTTY) {
		t.Fatal("managed terminal must stay interactive")
	}
	_ = got
}
