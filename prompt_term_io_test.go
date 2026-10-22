// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"io"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestThemedInputProbesBeforeReadingKeys(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	useTheme(t, NewTheme())
	// keys typed during the probe stay with the prompt
	tt, term := probeTermIO(t, oscReply+"a\r")
	tt.Restore = func() error { return nil }
	i := newInput(t.Context(), "Label")
	i.in, i.out = tt.in, tt.out
	i.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) { return tt, nil }
	got, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
	assert.Equal(t, 1, term.count())
	assert.True(t, i.look.roles.foreground != nil) // measured, so neutrals render
}
