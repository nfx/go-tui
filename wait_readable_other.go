// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build !unix && !windows

package tui

import (
	"context"
	"io"
)

// Fallback for platforms without a readiness-polling implementation:
// keep existing behavior without fd readiness polling.
func waitForReadableInput(context.Context, io.Reader) error {
	return nil
}

func canDrainOnCancel(io.Reader) bool {
	return false
}
