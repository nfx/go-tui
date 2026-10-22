// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"io"
	"os"
)

// Stderr returns the package-default append-only terminal writer.
//
// On interactive TTYs this routes writes through go-tui's shared terminal
// arbiter so prompts and external output are ordered by one renderer. On
// non-TTY outputs it returns the configured raw writer directly.
func Stderr() io.Writer {
	return defaultIO.stderrWriter()
}

// SetDefaultIO overrides package-wide default input/output used by widgets that
// were not explicitly configured with [WithInput] or [WithOutput].
//
// Deprecated: prefer the package defaults plus [Stderr] for normal CLI + TUI
// integration. This remains for tests and explicit advanced overrides.
func SetDefaultIO(in io.Reader, out io.Writer) {
	defaultIO.setIO(in, out)
}

// Deprecated: use [Stderr].
func NewIO(ctx context.Context) (*chanIO, error) {
	return startIO(ctx, os.Stderr)
}
