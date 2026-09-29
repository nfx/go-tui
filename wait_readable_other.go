// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build !unix

package tui

import (
	"context"
	"io"
)

// Non-unix fallback: keep existing behavior without fd readiness polling.
func waitForReadableInput(context.Context, io.Reader) error {
	return nil
}
