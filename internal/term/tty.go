// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"io"

	"golang.org/x/term"
)

var InputChecker = func(fd int) bool {
	return term.IsTerminal(fd)
}

// OutputIsTerminal reports whether prompts can draw on out. Without a terminal
// every prompt answers from its default or fails with [ErrNoTTY]. Tests that
// drive the interactive path with in-memory writers replace it.
var OutputIsTerminal = HostsTerminal

// HostsTerminal accepts a managed terminal or a descriptor of a real one. A
// writer without terminal capabilities, such as [bytes.Buffer], a pipe or a
// redirected file, is not a terminal, however the input is wrapped. Neither is
// a real one in CI, where nobody answers prompts.
func HostsTerminal(out io.Writer) bool {
	switch w := out.(type) {
	case *ChanIO, *Viewport, *Tui:
		return true
	case Descriptor:
		if Default.Env.Ci {
			return false
		}
		_, _, err := GetSize(int(w.Fd()))
		return err == nil
	}
	return false
}
