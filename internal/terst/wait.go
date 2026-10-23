// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"testing"
	"time"
)

// timeout is how long every helper waits before it fails the test.
const timeout = 2 * time.Second

// Recv returns the next value from ch, failing the test after the timeout.
func Recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(timeout):
		var zero T
		t.Fatalf("timed out receiving %T", zero)
		return zero
	}
}

// Send delivers v on ch, failing the test after the timeout.
func Send[T any](t *testing.T, ch chan<- T, v T) {
	t.Helper()
	select {
	case ch <- v:
	case <-time.After(timeout):
		t.Fatalf("timed out sending %T", v)
	}
}
