// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build linux

package tui_test

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openPty opens a new pseudo-terminal pair.
func openPty() (master, slave *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	var n uint32
	err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0)
	if err == nil {
		n, err = unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	}
	if err == nil {
		slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	}
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
