// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build unix

package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var (
	resizeOnce sync.Once
	resizeCh   chan struct{}
)

// resizeNotify returns a channel that receives a value
// each time the terminal window size changes (SIGWINCH).
func resizeNotify() <-chan struct{} {
	resizeOnce.Do(func() {
		resizeCh = make(chan struct{}, 1)
		sigC := make(chan os.Signal, 1)
		signal.Notify(sigC, syscall.SIGWINCH)
		go func() {
			for range sigC {
				select {
				case resizeCh <- struct{}{}:
				default: // don't block if nobody is listening
				}
			}
		}()
	})
	return resizeCh
}

// canDrainOnCancel reports whether a read started after [waitForReadableInput]
// returns promptly once ctx is cancelled: only files are polled, and poll
// failures are returned as errors instead of falling through to a blocking read.
func canDrainOnCancel(in io.Reader) bool {
	_, ok := in.(*os.File)
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
	pollFds := []unix.PollFd{{
		Fd:     int32(d.Fd()),
		Events: unix.POLLIN,
	}}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// round up so that a sub-millisecond remainder does not spin
		_, err := unix.Poll(pollFds, int((pollWait(ctx)+time.Millisecond-1)/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if pollFds[0].Revents&unix.POLLNVAL != 0 {
			return unix.EBADF
		}
		if pollFds[0].Revents&(unix.POLLIN|unix.POLLERR|unix.POLLHUP) != 0 {
			return nil
		}
	}
}
