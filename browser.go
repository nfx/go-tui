// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

var (
	browserExecCommand    = exec.Command
	browserGOOS           = runtime.GOOS
	browserCommandStarter = func(cmd *exec.Cmd) error { return cmd.Start() }
)

// Browserf opens the specified URL in the default browser.
func Browserf(addr string, args ...any) error {
	var cmd *exec.Cmd
	for i := range args {
		if s, ok := args[i].(string); ok {
			args[i] = url.QueryEscape(s)
		}
	}
	addr = fmt.Sprintf(addr, args...)
	switch browserGOOS {
	case "linux":
		cmd = browserExecCommand("xdg-open", addr)
	case "windows":
		cmd = browserExecCommand("rundll32", "url.dll,FileProtocolHandler", addr)
	case "darwin": // macOS
		cmd = browserExecCommand("open", addr)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedPlatform, browserGOOS)
	}
	return browserCommandStarter(cmd)
}
