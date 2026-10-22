// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"os"
	"testing"
)

func TestTuiViewAndPrepend(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio := newUnstartedIO(ctx, 10, 2, 0)
	tui := &Tui{ctx: ctx, termIO: &termIO{out: cio}}
	if tui.view() != cio.head {
		t.Fatalf("expected head viewport")
	}
	top := tui.prependView(1)
	if tui.view() != top {
		t.Fatalf("expected prepended viewport")
	}
}

func TestNewTUIReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err := NewTUI(ctx)
	if err != nil {
		return
	}
}

func TestNewTUIWithTTY(t *testing.T) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		t.Skip("tty not available")
	}
	origStderr := os.Stderr
	os.Stderr = tty
	t.Cleanup(func() {
		os.Stderr = origStderr
		if err := tty.Close(); err != nil {
			t.Fatalf("close tty: %v", err)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err = NewTUI(ctx)
	if err != nil {
		t.Fatalf("NewTUI failed: %v", err)
	}
}

func TestNewTUIWithStubSize(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	t.Cleanup(func() { termGetSize = orig })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err := NewTUI(ctx)
	if err != nil {
		t.Fatalf("NewTUI failed: %v", err)
	}
}

func TestTuiViewReturnsNilWithoutChanIO(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	tui := &Tui{ctx: ctx, termIO: &termIO{out: &bytes.Buffer{}}}
	if tui.view() != nil {
		t.Fatalf("expected nil viewport")
	}
}
