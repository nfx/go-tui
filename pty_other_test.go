// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build !darwin && !linux

package tui_test

import (
	"errors"
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

// openPty reports unsupported platforms so terminal tests can skip cleanly.
func openPty() (master, slave *os.File, err error) {
	return nil, nil, errors.New("pseudo-terminals are only opened on darwin and linux")
}

// setTTYSize skips resize-dependent tests on unsupported platforms.
func setTTYSize(t *testing.T, _ *os.File, _, _ int) {
	t.Helper()
	t.Skip("tty size is only set on darwin and linux")
}
