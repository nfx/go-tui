// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/theme"
	"testing"
	"time"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/terst"
)

func TestProgressbarUpdatesAfterCompletion(t *testing.T) {
	term := newTerminal(t, 6)
	p, err := tui.NewMaxProgressBar(t.Context(), "Copy", 4)
	assert.NoError(t, err)
	p.Add(2)
	term.AwaitVisible("Copy  50%  ...", "2 / 4 items...")
	p.Add(5) // overshoots the maximum
	// the bar completes on its next redraw and disappears
	term.AwaitVisible()
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
	term.AwaitVisible("after")
}

func TestProgressbarStacksOnTwoRowTerminal(t *testing.T) {
	term := newTerminal(t, 2)
	term.Resize(40, 2)
	terst.UseASCII(t)
	// through the managed viewport, two rows hold the bar and its hint
	p, err := tui.NewMaxProgressBar(t.Context(), "Copy", 4)
	assert.NoError(t, err)
	p.Add(1)
	term.AwaitVisible("Copy  25%  ...", "1 / 4 items...")
	p.Add(1)
	term.AwaitVisible("Copy  50%  ...", "2 / 4 items...")
	assert.NoError(t, p.Close())
	term.AwaitVisible()
}

func TestSpinnerUpdatesAfterCompletion(t *testing.T) {
	term := newTerminal(t, 6)
	th := theme.New()
	th.Symbols.Spinner = []string{"-"}
	terst.UseTheme(t, th)
	s, err := tui.NewSpinners(t.Context(), tui.WithNoElapsed())
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
	term.AwaitVisible("✖ fetch: timeout", "■ build: done", "- sentinel")
}

// A terminal that does not join emoji draws a ZWJ family wider than the two
// columns it is measured at, so a clipped row overflows. It must not wrap, or
// each redraw leaves the top row of the last frame on the screen.
func TestSpinnerWideClusterLeavesNoStaleRows(t *testing.T) {
	term := newTerminal(t, 6)
	th := theme.New()
	th.Symbols.Spinner = []string{"-"}
	terst.UseTheme(t, th)
	s, err := tui.NewSpinners(t.Context(), tui.WithNoElapsed())
	assert.NoError(t, err)
	t.Cleanup(s.Close)
	build, err := s.Add(t.Context(), tui.WithPrefixf("build"))
	assert.NoError(t, err)
	web, err := s.Add(t.Context(), tui.WithPrefixf("web"))
	assert.NoError(t, err)
	web.Update("\U0001F468\u200d\U0001F469\u200d\U0001F467 restarting the service with a long tail")
	for i := range 3 {
		build.Updatef("building %d", i)
		term.AwaitVisible(
			fmt.Sprintf("- build: building %d", i),
			"- web: \U0001F468\u200d\U0001F469\u200d\U0001F467 restarting...",
		)
	}
}
