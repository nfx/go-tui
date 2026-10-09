// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build !darwin && !linux

package tui_test

import (
	"os"
	"testing"
)

func openTTY(t *testing.T, _, _ int) *os.File {
	t.Helper()
	t.Skip("pseudo-terminals are only opened on darwin and linux")
	return nil
}

func resizeTTY(t *testing.T, _ *os.File, _, _ int) {
	t.Helper()
	t.Skip("resize is only signalled on darwin and linux")
}
