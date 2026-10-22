// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

type progressEvent interface {
	isProgressOutgoing()
}

type progressEventMarker struct{}

func (progressEventMarker) isProgressOutgoing() {}

type progressInit struct {
	progressEventMarker
	Label string
	Max   int64
}

type progressUpdate struct {
	progressEventMarker
	Complete  float64 // 0..1
	Rate      float64 // per second
	Remaining int64   // seconds; -1 when no rate is available yet
	Elapsed   int64   // seconds
}

type progressClosed struct {
	progressEventMarker
	Label string
}
