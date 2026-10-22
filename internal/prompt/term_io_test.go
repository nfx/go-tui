// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

import (
	"io"
	"testing"

	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestThemedInputProbesBeforeReadingKeys(t *testing.T) {
	terst.WithEnv(t, "FORCE_COLOR=3")
	terst.UseTheme(t, theme.New())
	// keys typed during the probe stay with the prompt
	tt, rt := terst.ProbeTermIO(t, terst.OSCReply+"a\r")
	tt.Restore = func() error { return nil }
	i := newInput(t.Context(), "Label")
	i.In, i.Out = tt.In, tt.Out
	i.makeTermIO = func(io.Reader, io.Writer) (*term.IO, error) { return tt, nil }
	got, err := i.read(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "a", got)
	assert.Equal(t, 1, rt.Count())
	assert.True(t, i.look.Roles.Foreground != nil) // measured, so neutrals render
}
