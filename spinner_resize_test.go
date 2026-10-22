// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/term"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestSpinnerRedrawTruncatesLongMessage(t *testing.T) {
	s := newSpinners(t.Context())
	s.io = &term.IO{
		Out:     &bytes.Buffer{},
		Width:   20,
		Height:  4,
		Restore: func() error { return nil },
	}
	s.state = []*spinnerState{
		{
			frames:  []string{"..."},
			Message: ansi.Text("this is a very long spinner message that should be truncated"),
			tick:    0,
		},
	}
	prevActive, err := s.redraw(0)
	assert.NoError(t, err)
	assert.Equal(t, 1, prevActive)
	out, ok := s.io.Out.(*bytes.Buffer)
	assert.True(t, ok)
	lines := bytes.Split(out.Bytes(), []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		// each line must fit within the full width = 20
		assert.True(t, ansi.Text(line).Width() <= 20)
	}
}
