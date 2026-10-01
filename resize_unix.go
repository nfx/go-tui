// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

//go:build unix

package tui

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
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
