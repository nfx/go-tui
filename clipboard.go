// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

var clipboardExecCommand = exec.Command
var clipboardCommandOutput = func(cmd *exec.Cmd) ([]byte, error) {
	return cmd.Output()
}

type clipboard struct{}

func ShouldPasteFromClipboard() string {
	content, _ := (&clipboard{}).Read() //nolint:errcheck // ignore clipboard errors
	return content
}

func (cr *clipboard) Read() (string, error) {
	cmd, err := cr.pasteCommand()
	if err != nil {
		return "", fmt.Errorf("paste command: %w", err)
	}
	output, err := clipboardCommandOutput(cmd)
	if err != nil {
		return "", fmt.Errorf("run: %w", err)
	}
	content := strings.TrimRight(string(output), "\n\r")
	return content, nil
}

var clipboardPasteImplementations = map[string][][]string{
	"darwin": {
		{"pbpaste"},
	},
	"linux": {
		{"xclip", "-selection", "clipboard", "-o"},
		{"xsel", "--clipboard", "--output"},
		{"wl-paste"},
	},
	"windows": {
		{"powershell", "-command", "Get-Clipboard"},
	},
}

func (cr *clipboard) pasteCommand() (*exec.Cmd, error) {
	impls, ok := clipboardPasteImplementations[runtime.GOOS]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedPlatform, runtime.GOOS)
	}
	for _, args := range impls {
		_, err := exec.LookPath(args[0])
		if err != nil {
			continue
		}
		return clipboardExecCommand(args[0], args[1:]...), nil
	}
	return nil, fmt.Errorf("%w: no clipboard paste utility found", ErrUnsupportedPlatform)
}
