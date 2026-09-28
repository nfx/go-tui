// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestClipboardRead(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.SkipNow()
	}
	origExec := clipboardExecCommand
	origOutput := clipboardCommandOutput
	t.Cleanup(func() {
		clipboardExecCommand = origExec
		clipboardCommandOutput = origOutput
	})

	var receivedCmd *exec.Cmd
	clipboardExecCommand = func(name string, args ...string) *exec.Cmd {
		return &exec.Cmd{
			Path: name,
			Args: append([]string{name}, args...),
		}
	}
	clipboardCommandOutput = func(cmd *exec.Cmd) ([]byte, error) {
		receivedCmd = cmd
		return []byte("line1\n"), nil
	}

	content, err := (&clipboard{}).Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "line1" {
		t.Fatalf("expected trimmed output, got %q", content)
	}
	if receivedCmd == nil || receivedCmd.Path == "" {
		t.Fatalf("command not built")
	}
}

func TestShouldPasteFromClipboardIgnoresError(t *testing.T) {
	origOutput := clipboardCommandOutput
	t.Cleanup(func() {
		clipboardCommandOutput = origOutput
	})
	clipboardCommandOutput = func(cmd *exec.Cmd) ([]byte, error) {
		return nil, bytes.ErrTooLarge
	}

	if got := ShouldPasteFromClipboard(); got != "" {
		t.Fatalf("expected empty string on error, got %q", got)
	}
}

func TestClipboardPasteCommandSuccess(t *testing.T) {
	orig := clipboardPasteImplementations
	t.Cleanup(func() { clipboardPasteImplementations = orig })
	clipboardPasteImplementations = map[string][][]string{
		runtime.GOOS: {{"go", "env"}},
	}
	cmd, err := (&clipboard{}).pasteCommand()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Path == "" {
		t.Fatalf("expected command path")
	}
}

func TestClipboardPasteCommandMissing(t *testing.T) {
	orig := clipboardPasteImplementations
	t.Cleanup(func() { clipboardPasteImplementations = orig })
	clipboardPasteImplementations = map[string][][]string{
		runtime.GOOS: {{"definitely-missing-cmd"}},
	}
	_, err := (&clipboard{}).pasteCommand()
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected unsupported platform error, got %v", err)
	}
}
