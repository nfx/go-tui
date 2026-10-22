// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"strconv"
)

// segment is a part of text that is never split.
//
// It is a cluster (one or more runes shown as 0, 1 or 2 columns) unless one of
// the flags is set.
type segment struct {
	text  []byte
	width int // terminal columns

	// isInvalid is set for a single byte that is not UTF-8.
	isInvalid bool
	// isControl is set for a control character, like \n, \t or DEL.
	isControl bool
	// isEscape is set for a complete escape sequence.
	isEscape bool
	// isIncomplete is set for an escape sequence cut by the end of input.
	isIncomplete bool
	// isBroken is set for a lone ESC, or an escape sequence cancelled by a byte
	// that cannot belong to it, so it would swallow the text appended after it.
	isBroken bool
}

// isText reports whether t is printable text rather than terminal control data.
func (t segment) isText() bool {
	return !t.isInvalid && !t.isControl && !t.isEscape && !t.isIncomplete && !t.isBroken
}

// sgrStyle reports whether t is SGR and may leave a style open.
func (t segment) sgrStyle() (styled, ok bool) {
	params, ok := t.sgrParams()
	if !ok {
		return false, false
	}
	return len(bytes.Trim(params, "0;")) > 0, true
}

// sgrParams returns SGR parameters, rejecting non-SGR sequences.
func (t segment) sgrParams() (text, bool) {
	params, ok := bytes.CutPrefix(t.text, []byte("\x1b["))
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

// sgrChannel is a part of the style that SGR sets and resets independently.
type sgrChannel uint8

const (
	sgrForeground sgrChannel = 1 << iota
	sgrBackground
	sgrIntensity // bold and dim share reset 22
	sgrItalic
	sgrUnderline
	sgrStrike

	sgrAll = sgrForeground | sgrBackground | sgrIntensity | sgrItalic | sgrUnderline | sgrStrike
)

// sgrEffect reports final channel changes without treating color arguments as resets.
func (t segment) sgrEffect() (set, cleared sgrChannel) {
	params, ok := t.sgrParams()
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
func (t segment) sgrCodeEffect(code int) (on, off sgrChannel) {
	switch {
	case code == 0:
		return 0, sgrAll
	case code == 1 || code == 2:
		return sgrIntensity, 0
	case code == 3:
		return sgrItalic, 0
	case code == 4:
		return sgrUnderline, 0
	case code == 9:
		return sgrStrike, 0
	case code == 22:
		return 0, sgrIntensity
	case code == 23:
		return 0, sgrItalic
	case code == 24:
		return 0, sgrUnderline
	case code == 29:
		return 0, sgrStrike
	case code >= 30 && code <= 38, code >= 90 && code <= 97:
		return sgrForeground, 0
	case code == 39:
		return 0, sgrForeground
	case code >= 40 && code <= 48, code >= 100 && code <= 107:
		return sgrBackground, 0
	case code == 49:
		return 0, sgrBackground
	}
	return 0, 0
}

// osc8 returns the URI of an OSC 8 sequence, and whether t is one.
func (t segment) osc8() (uri []byte, ok bool) {
	body, ok := bytes.CutPrefix(t.text, []byte("\x1b]8;"))
	if !ok {
		return nil, false
	}
	body = bytes.TrimSuffix(bytes.TrimSuffix(body, []byte("\a")), []byte("\x1b\\"))
	_, uri, ok = bytes.Cut(body, []byte(";"))
	return uri, ok
}

// isHyperlink reports whether t is an OSC 8 sequence that opens a link.
func (t segment) isHyperlink() bool {
	uri, ok := t.osc8()
	return ok && len(uri) > 0
}

// endsHyperlink reports whether t is an OSC 8 sequence that closes a link.
func (t segment) endsHyperlink() bool {
	uri, ok := t.osc8()
	return ok && len(uri) == 0
}
