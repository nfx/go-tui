// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// backgroundProbeSupported excludes Windows consoles without VT input enabled.
var backgroundProbeSupported = runtime.GOOS != "windows"

// canProbe checks for terminal input and a control output path.
func (t *termIO) canProbe() bool {
	in, ok := t.in.(descriptor)
	if !ok || !backgroundProbeSupported || !terminalInputChecker(int(in.Fd())) {
		return false
	}
	return t.cio != nil || t.fd > 0
}

// detectBackground uses the existing input owner for OSC 11, then falls back
// to hint, the COLORFGBG estimate or dark. [tio.background] handles caching.
func (t *termIO) detectBackground(ctx context.Context, hint Background) Background {
	if t.canProbe() {
		if b, ok := t.probeBackground(ctx); ok {
			return b
		}
	}
	return hint
}

// backgroundProbeTimeout bounds each OSC 11 query.
const backgroundProbeTimeout = 200 * time.Millisecond

// probeBackground queries OSC 11 without consuming keys; invalid replies fail.
func (t *termIO) probeBackground(ctx context.Context) (Background, bool) {
	ctx, cancel := context.WithTimeout(ctx, backgroundProbeTimeout)
	// Register before reading to catch fast replies; drain and replay intervening keys.
	q := t.expectControl(11)
	defer q.cancel()
	permits := make(chan struct{})
	keys := t.readEvents(ctx, permits)
	var typed []keyEvent
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range keys {
			// Readiness failures carry no input; every read-backed event, including
			// paste and Ctrl-C/D errors, was acknowledged and must be replayed.
			if ev.readOrder == 0 {
				continue
			}
			if ev.bracketed {
				ev.err = nil // undo [termIO.shapeEvent] so replay reshapes for its consumer
			}
			typed = append(typed, ev)
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case permits <- struct{}{}:
			}
		}
	}()
	defer func() {
		cancel()
		t.awaitReader()
		<-drained
		t.restoreKeys(typed)
	}()
	err := t.writeControl(ctx, []byte("\x1b]11;?\x07"))
	if err != nil {
		return Background{}, false
	}
	select {
	case <-ctx.Done():
		return Background{}, false
	case reply := <-q.reply:
		rgb, ok := t.parseOSCColor(reply.data)
		if !ok {
			return Background{}, false
		}
		return Background{
			RGB:      rgb,
			Light:    rgb.IsLight(),
			Measured: true,
		}, true
	}
}

// parseOSCColor scales xterm rgb:R/G/B components of one to four hex digits to bytes.
func (t *termIO) parseOSCColor(data string) (rgb SRGB, ok bool) {
	spec, found := strings.CutPrefix(data, "rgb:")
	if !found {
		return rgb, false
	}
	parts := strings.Split(spec, "/")
	if len(parts) != 3 {
		return rgb, false
	}
	for i, p := range parts {
		if len(p) < 1 || len(p) > 4 {
			return rgb, false
		}
		v, err := strconv.ParseUint(p, 16, 16)
		if err != nil {
			return rgb, false
		}
		maxV := uint64(1)<<(4*len(p)) - 1
		rgb[i] = uint8((v*255 + maxV/2) / maxV)
	}
	return rgb, true
}

// parseColorFGBG estimates the background from fg;bg or fg;xpm;bg using the ANSI palette.
func (t *termIO) parseColorFGBG(v string) (rgb SRGB, ok bool) {
	fields := strings.Split(v, ";")
	if len(fields) < 2 || len(fields) > 3 {
		return rgb, false
	}
	i, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil || i < 0 || i > 255 {
		return rgb, false
	}
	return ANSIPalette()[i], true
}

// backgroundFromEnv classifies COLORFGBG, or falls back to dark.
func (t *termIO) backgroundFromEnv(lookup func(string) (string, bool)) Background {
	if lookup == nil {
		return Background{}
	}
	v, _ := lookup("COLORFGBG")
	rgb, ok := t.parseColorFGBG(v)
	if !ok {
		return Background{}
	}
	return Background{Light: rgb.IsLight()}
}
