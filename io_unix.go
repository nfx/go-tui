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
	pollFds := []unix.PollFd{{
		Fd:     int32(d.Fd()),
		Events: unix.POLLIN,
	}}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err := unix.Poll(pollFds, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if pollFds[0].Revents&(unix.POLLIN|unix.POLLERR|unix.POLLHUP) != 0 {
			return nil
		}
	}
}
