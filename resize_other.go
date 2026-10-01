// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build !unix

package tui

// resizeNotify returns nil on non-unix platforms
// where SIGWINCH is not available.
func resizeNotify() <-chan struct{} {
	return nil
}
