// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package spinner

type spinnerEvent interface {
	isSpinnerOutgoing()
}

type spinnerEventMarker struct{}

func (spinnerEventMarker) isSpinnerOutgoing() {}

type spinnerGroupInit struct {
	spinnerEventMarker
}

type spinnerAdded struct {
	spinnerEventMarker
	Name   string
	Index  int
	Parent int // -1 for a top-level spinner
}

type spinnerUpdated struct {
	spinnerEventMarker
	Index   int
	Message string
}

type spinnerFailed struct {
	spinnerEventMarker
	Index int
	Error string
}

type spinnerRemoved struct {
	spinnerEventMarker
	Index int
}

type spinnerGroupClosed struct {
	spinnerEventMarker
}
