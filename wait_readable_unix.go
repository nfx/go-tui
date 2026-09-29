// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build unix

package tui

import (
	"context"
	"errors"
	"io"

	"golang.org/x/sys/unix"
)

func canDrainOnCancel(in io.Reader) bool {
	_, ok := in.(descriptor)
	return ok
}

func waitForReadableInput(ctx context.Context, in io.Reader) error {
	d, ok := in.(descriptor)
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
