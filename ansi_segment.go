// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"strconv"
)

// Segment is a part of text that is never split.
//
// It is a cluster (one or more runes shown as 0, 1 or 2 columns) unless one of
// the flags is set.
type Segment struct {
	Text  []byte
	Width int // terminal columns

	// IsInvalid is set for a single byte that is not UTF-8.
	IsInvalid bool
	// IsControl is set for a control character, like \n, \t or DEL.
	IsControl bool
	// IsEscape is set for a complete escape sequence.
	IsEscape bool
	// IsIncomplete is set for an escape sequence cut by the end of input.
	IsIncomplete bool
	// IsBroken is set for a lone ESC, or an escape sequence cancelled by a byte
	// that cannot belong to it, so it would swallow the text appended after it.
	IsBroken bool
}

// IsText reports whether t is printable text rather than terminal control data.
func (t Segment) IsText() bool {
	return !t.IsInvalid && !t.IsControl && !t.IsEscape && !t.IsIncomplete && !t.IsBroken
}

// sgrStyle reports whether t is SGR and may leave a style open.
func (t Segment) sgrStyle() (styled, ok bool) {
	params, ok := t.SGRParams()
	if !ok {
		return false, false
	}
	return len(bytes.Trim(params, "0;")) > 0, true
}

// SGRParams returns SGR parameters, rejecting non-SGR sequences.
func (t Segment) SGRParams() (Text, bool) {
	params, ok := bytes.CutPrefix(t.Text, []byte("\x1b["))
	if !ok {
		return nil, false
	}
	params, ok = bytes.CutSuffix(params, []byte("m"))
	// private parameters, like in ESC [ > 4 m, are not SGR
	if !ok || len(bytes.Trim(params, "0123456789;:")) > 0 {
		return nil, false
	}
	return params, true
}

// SGRChannel is a part of the style that SGR sets and resets independently.
type SGRChannel uint8

const (
	SGRForeground SGRChannel = 1 << iota
	SGRBackground
	SGRIntensity // bold and dim share reset 22
	SGRItalic
	SGRUnderline
	SGRStrike

	SGRAll = SGRForeground | SGRBackground | SGRIntensity | SGRItalic | SGRUnderline | SGRStrike
)

// SGREffect reports final channel changes without treating color arguments as resets.
func (t Segment) SGREffect() (set, cleared SGRChannel) {
	params, ok := t.SGRParams()
	if !ok {
		return 0, 0
	}
	fields := bytes.Split(params, []byte(";"))
	for i := 0; i < len(fields); i++ {
		head, _, colon := bytes.Cut(fields[i], []byte(":"))
		code := -1
		if len(head) == 0 {
			code = 0 // an empty parameter is a reset
		} else if n, err := strconv.Atoi(string(head)); err == nil {
			code = n
		}
		on, off := t.sgrCodeEffect(code)
		set, cleared = (set|on)&^off, (cleared|off)&^on
		if (code == 38 || code == 48 || code == 58) && !colon && i+1 < len(fields) {
			switch string(fields[i+1]) {
			case "5":
				i += 2
			case "2":
				i += 4
			}
		}
	}
	return set, cleared
}

// sgrCodeEffect returns the channels one SGR parameter sets and resets.
func (t Segment) sgrCodeEffect(code int) (on, off SGRChannel) {
	switch {
	case code == 0:
		return 0, SGRAll
	case code == 1 || code == 2:
		return SGRIntensity, 0
	case code == 3:
		return SGRItalic, 0
	case code == 4:
		return SGRUnderline, 0
	case code == 9:
		return SGRStrike, 0
	case code == 22:
		return 0, SGRIntensity
	case code == 23:
		return 0, SGRItalic
	case code == 24:
		return 0, SGRUnderline
	case code == 29:
		return 0, SGRStrike
	case code >= 30 && code <= 38, code >= 90 && code <= 97:
		return SGRForeground, 0
	case code == 39:
		return 0, SGRForeground
	case code >= 40 && code <= 48, code >= 100 && code <= 107:
		return SGRBackground, 0
	case code == 49:
		return 0, SGRBackground
	}
	return 0, 0
}

// OSC8 returns the URI of an OSC 8 sequence, and whether t is one.
func (t Segment) OSC8() (uri []byte, ok bool) {
	body, ok := bytes.CutPrefix(t.Text, []byte("\x1b]8;"))
	if !ok {
		return nil, false
	}
	body = bytes.TrimSuffix(bytes.TrimSuffix(body, []byte("\a")), []byte("\x1b\\"))
	_, uri, ok = bytes.Cut(body, []byte(";"))
	return uri, ok
}

// isHyperlink reports whether t is an OSC 8 sequence that opens a link.
func (t Segment) isHyperlink() bool {
	uri, ok := t.OSC8()
	return ok && len(uri) > 0
}

// endsHyperlink reports whether t is an OSC 8 sequence that closes a link.
func (t Segment) endsHyperlink() bool {
	uri, ok := t.OSC8()
	return ok && len(uri) == 0
}
