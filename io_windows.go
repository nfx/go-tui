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

var (
	k32                  = windows.NewLazySystemDLL("kernel32.dll")
	procPeekNamedPipe    = k32.NewProc("PeekNamedPipe")
	procPeekConsoleInput = k32.NewProc("PeekConsoleInputW")
	procReadConsoleInput = k32.NewProc("ReadConsoleInputW")
)

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

// canDrainOnCancel reports whether [waitForReadableInput] can interrupt the
// wait for this specific input, so a following read never blocks past cancellation.
func canDrainOnCancel(in io.Reader) bool {
	d, ok := in.(*os.File)
	if !ok {
		return false
	}
	h := windows.Handle(d.Fd())
	ft, err := windows.GetFileType(h)
	if err != nil {
		return false
	}
	switch ft {
	case windows.FILE_TYPE_PIPE:
		_, err := peekNamedPipe(h)
		return err == nil
	case windows.FILE_TYPE_CHAR:
		var mode uint32
		return windows.GetConsoleMode(h, &mode) == nil
	default:
		return false
	}
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

const (
	keyEventType   = 0x0001
	maxConsolePeek = 64
)

// inputRecord mirrors INPUT_RECORD; only the KEY_EVENT_RECORD layout is decoded.
type inputRecord struct {
	EventType uint16
	_         uint16
	KeyDown   int32
	_         [6]byte // repeat count, virtual key code and scan code
	Char      uint16
	_         [4]byte // control key state
}

// consoleHasChar drops queued records that character reads would discard
// (mouse, resize, focus, key-up, non-character keys) and reports whether
// the head of the queue is a character key press, which a read returns without blocking.
func consoleHasChar(h windows.Handle) (bool, error) {
	var recs [maxConsolePeek]inputRecord
	var n uint32
	r1, _, e1 := procPeekConsoleInput.Call(uintptr(h),
		uintptr(unsafe.Pointer(&recs[0])), maxConsolePeek, uintptr(unsafe.Pointer(&n)))
	if r1 == 0 {
		return false, e1
	}
	skip := uint32(0)
	for skip < n {
		r := recs[skip]
		if r.EventType == keyEventType && r.KeyDown != 0 && r.Char != 0 {
			break
		}
		skip++
	}
	if skip > 0 {
		var read uint32
		r1, _, e1 = procReadConsoleInput.Call(uintptr(h),
			uintptr(unsafe.Pointer(&recs[0])), uintptr(skip), uintptr(unsafe.Pointer(&read)))
		if r1 == 0 {
			return false, e1
		}
	}
	return skip < n, nil
}

func waitForConsoleReadable(ctx context.Context, h windows.Handle) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		event, err := windows.WaitForSingleObject(h, uint32((pollWait(ctx)+time.Millisecond-1)/time.Millisecond))
		if err != nil {
			return nil
		}
		if event != windows.WAIT_OBJECT_0 {
			continue
		}
		ok, err := consoleHasChar(h)
		if err != nil {
			// not a console input buffer; fall back to a blocking read.
			return nil
		}
		if ok {
			return nil
		}
	}
}
