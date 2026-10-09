// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build darwin

package tui_test

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty opens a new pseudo-terminal pair.
func openPty() (master, slave *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	var name [128]byte
	err = unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0)
	if err == nil {
		err = unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0)
	}
	if err == nil {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd),
			uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0])))
		if errno != 0 {
			err = errno
		}
	}
	if err == nil {
		slave, err = os.OpenFile(unix.ByteSliceToString(name[:]), os.O_RDWR|unix.O_NOCTTY, 0)
	}
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
