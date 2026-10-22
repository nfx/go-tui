// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/assert"
)

func TestProgressbarUpdatesAfterCompletion(t *testing.T) {
	term := newTerminal(t, 6)
	p, err := tui.NewMaxProgressBar(t.Context(), "Copy", 4)
	assert.NoError(t, err)
	p.Add(2)
	term.waitRows(t, "Copy  50%  ...", "2 / 4 items...")
	p.Add(5) // overshoots the maximum
	// the bar completes on its next redraw and disappears
	term.waitRows(t)
	updated := make(chan struct{})
	go func() {
		defer close(updated)
		p.Add(1)
		p.Add(1)
	}()
	select {
	case <-updated:
	case <-time.After(failsafe):
		t.Fatal("updates block after completion")
	}
	assert.NoError(t, p.Close())
	assert.NoError(t, p.Close())
	p.Add(1)
	_, err = fmt.Fprintln(tui.Stderr(), "after")
	assert.NoError(t, err)
	term.waitRows(t, "after")
}

func TestProgressbarOnOutputKeepsBarOnShortTerminal(t *testing.T) {
	term := newTerminal(t, 2)
	term.height = 2
	asciiTheme(t)
	// a descriptor of its own skips the managed viewport, so the cursor takes
	// the row below the frame and a stacked frame would scroll the bar off
	out := &ttyWriter{fd: term.tty.Fd(), screen: term.screen}
	p, err := tui.NewMaxProgressBar(t.Context(), "Copy", 4, tui.WithOutput(out))
	assert.NoError(t, err)
	p.Add(1)
	term.waitRows(t, "Copy  25%  ...")
	p.Add(1)
	term.waitRows(t, "Copy  50%  ...")
	assert.NoError(t, p.Close())
	term.waitRows(t)
}

func TestProgressbarStacksOnTwoRowTerminal(t *testing.T) {
	term := newTerminal(t, 2)
	term.height = 2
	asciiTheme(t)
	// through the managed viewport, two rows hold the bar and its hint
	p, err := tui.NewMaxProgressBar(t.Context(), "Copy", 4)
	assert.NoError(t, err)
	p.Add(1)
	term.waitRows(t, "Copy  25%  ...", "1 / 4 items...")
	p.Add(1)
	term.waitRows(t, "Copy  50%  ...", "2 / 4 items...")
	assert.NoError(t, p.Close())
	term.waitRows(t)
}

func TestSpinnerUpdatesAfterCompletion(t *testing.T) {
	term := newTerminal(t, 6)
	th := tui.NewTheme()
	th.Symbols.Spinner = []string{"-"}
	tui.UseTheme(t, th)
	s, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	t.Cleanup(s.Close)
	failed, err := s.Add(t.Context(), tui.WithPrefixf("fetch"))
	assert.NoError(t, err)
	kept, err := s.Add(t.Context(), tui.WithPrefixf("build"), tui.WithKeep())
	assert.NoError(t, err)
	failed.Fail(errors.New("timeout"))
	kept.Update("done")
	assert.NoError(t, kept.Close())
	// updates after completion do not change what was reported
	failed.Update("retrying")
	kept.Update("restarted")
	// spinners apply changes in order, so a frame with this one has all of them
	_, err = s.Add(context.Background(), tui.WithPrefixf("sentinel"))
	assert.NoError(t, err)
	term.waitRows(t, "✖ fetch: timeout", "■ build: done", "- sentinel:")
}
