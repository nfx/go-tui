// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/term"
	"io"
	"strings"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

// installedFuncs are the helpers of the installed theme, for a plain destination.
func installedFuncs() template.FuncMap {
	return term.Default.CurrentTheme().Funcs(color.ProfilePlain, render.Roles{})
}

// execute renders src with funcs and fails the test on any error.
func execute(t *testing.T, funcs template.FuncMap, src string) string {
	t.Helper()
	tmpl, err := template.New("t").Funcs(funcs).Parse(src)
	assert.NoError(t, err)
	var b strings.Builder
	assert.NoError(t, tmpl.Execute(&b, nil))
	return b.String()
}

func TestChanIOFlushAndCloseReportWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cio := term.NewUnstartedIO(ctx, 80, 24, 0)
	go cio.HandleViewports(ctx)
	go cio.ForwardTo(ctx, terst.FailingWriter{Err: io.ErrShortWrite})
	tio := &term.Terminal{Arbiter: cio, Cancel: cancel}

	_, err := cio.Write([]byte("lost\n"))
	assert.NoError(t, err) // queueing is asynchronous
	assert.ErrorIs(t, cio.Flush(), io.ErrShortWrite)
	assert.ErrorIs(t, tio.Flush(), io.ErrShortWrite)
	assert.ErrorIs(t, tio.Close(), io.ErrShortWrite)
}
