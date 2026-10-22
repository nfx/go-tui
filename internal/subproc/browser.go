// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package subproc

import (
	"fmt"
	"github.com/nfx/go-tui/internal/errs"
	"net/url"
	"os/exec"
	"runtime"
)

const goosLinux = "linux"

var (
	browserExecCommand    = exec.Command
	browserGOOS           = runtime.GOOS
	browserCommandStarter = func(cmd *exec.Cmd) error { return cmd.Start() }
)

// Browserf opens the URL in the default browser.
func Browserf(addr string, args ...any) error {
	var cmd *exec.Cmd
	if len(args) > 0 {
		for i := range args {
			if s, ok := args[i].(string); ok {
				args[i] = url.QueryEscape(s)
			}
		}
		addr = fmt.Sprintf(addr, args...)
	}
	switch browserGOOS {
	case goosLinux:
		cmd = browserExecCommand("xdg-open", addr)
	case "windows":
		cmd = browserExecCommand("rundll32", "url.dll,FileProtocolHandler", addr)
	case "darwin": // macOS
		cmd = browserExecCommand("open", addr)
	default:
		return fmt.Errorf("%w: %s", errs.ErrUnsupportedPlatform, browserGOOS)
	}
	return browserCommandStarter(cmd)
}
