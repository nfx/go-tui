// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"os/exec"
	"testing"
)

func TestBrowserfLinux(t *testing.T) {
	origGOOS := browserGOOS
	origCommand := browserExecCommand
	origStarter := browserCommandStarter
	t.Cleanup(func() {
		browserGOOS = origGOOS
		browserExecCommand = origCommand
		browserCommandStarter = origStarter
	})

	var started bool
	var captured *exec.Cmd
	browserGOOS = goosLinux
	browserExecCommand = func(name string, args ...string) *exec.Cmd {
		return &exec.Cmd{
			Path: name,
			Args: append([]string{name}, args...),
		}
	}
	browserCommandStarter = func(cmd *exec.Cmd) error {
		started = true
		captured = cmd
		return nil
	}

	err := Browserf("http://example.com/%s", "space value")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !started {
		t.Fatalf("browser command never started")
	}
	if captured.Path != "xdg-open" {
		t.Fatalf("unexpected path %q", captured.Path)
	}
	if len(captured.Args) != 2 || captured.Args[1] != "http://example.com/space+value" {
		t.Fatalf("unexpected args %v", captured.Args)
	}
}

func TestBrowserfLinux_PreEncodedURLNoArgs(t *testing.T) {
	origGOOS := browserGOOS
	origCommand := browserExecCommand
	origStarter := browserCommandStarter
	t.Cleanup(func() {
		browserGOOS = origGOOS
		browserExecCommand = origCommand
		browserCommandStarter = origStarter
	})

	var captured *exec.Cmd
	browserGOOS = goosLinux
	browserExecCommand = func(name string, args ...string) *exec.Cmd {
		return &exec.Cmd{
			Path: name,
			Args: append([]string{name}, args...),
		}
	}
	browserCommandStarter = func(cmd *exec.Cmd) error {
		captured = cmd
		return nil
	}

	authURL := "https://auth.example.com/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fcallback&state=abc"
	err := Browserf(authURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(captured.Args) != 2 || captured.Args[1] != authURL {
		t.Fatalf("unexpected args %v", captured.Args)
	}
}

func TestBrowserfUnsypported(t *testing.T) {
	origGOOS := browserGOOS
	t.Cleanup(func() {
		browserGOOS = origGOOS
	})
	browserGOOS = "plan9"

	err := Browserf("http://example.com")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform, got %v", err)
	}
}
