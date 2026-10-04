// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build windows

package tui

import (
	"context"
	"io"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// resizeNotify returns nil on non-unix platforms
// where SIGWINCH is not available.
func resizeNotify() <-chan struct{} {
	return nil
}

var procPeekNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")

func peekNamedPipe(h windows.Handle) (avail uint32, err error) {
	var totalAvail uint32
	r1, _, e1 := procPeekNamedPipe.Call(
		uintptr(h), 0, 0, 0,
		uintptr(unsafe.Pointer(&totalAvail)), 0,
	)
	if r1 == 0 {
		return 0, e1
	}
	return totalAvail, nil
}

func canDrainOnCancel(in io.Reader) bool {
	_, ok := in.(descriptor)
	return ok
}

// waitForReadableInput polls concrete files until input is ready or ctx is cancelled.
// Other readers may buffer data independently of their descriptor and are not polled.
func waitForReadableInput(ctx context.Context, in io.Reader) error {
	// only a real file is backed by its fd; a wrapper exposing Fd() may buffer data the fd never sees
	d, ok := in.(*os.File)
	if !ok {
		return nil
	}
	h := windows.Handle(d.Fd())
	ft, err := windows.GetFileType(h)
	if err != nil {
		return nil
	}
	switch ft {
	case windows.FILE_TYPE_PIPE:
		return waitForPipeReadable(ctx, h)
	case windows.FILE_TYPE_CHAR:
		return waitForConsoleReadable(ctx, h)
	default:
		return nil
	}
}

const pipePollInterval = time.Millisecond

func waitForPipeReadable(ctx context.Context, h windows.Handle) error {
	ticker := time.NewTicker(pipePollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		avail, err := peekNamedPipe(h)
		if err != nil {
			// not a peekable pipe instance; fall back to a blocking read.
			return nil
		}
		if avail > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForConsoleReadable(ctx context.Context, h windows.Handle) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		event, err := windows.WaitForSingleObject(h, 50)
		if err != nil {
			return nil
		}
		if event == windows.WAIT_OBJECT_0 {
			return nil
		}
	}
}
