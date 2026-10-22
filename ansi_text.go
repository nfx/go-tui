// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"iter"
	"unicode/utf8"
)

const (
	// maxHeldBytes limits how much of an unterminated sequence is held back.
	maxHeldBytes = 4096
)

// text is text written to a terminal: UTF-8 with controls and escape sequences.
type text []byte //nolint:recvcheck // Write appends in place, the rest only read

// String returns t as a string.
func (t text) String() string {
	return string(t)
}

// Write appends p to t, so that templates and formatters render into it.
func (t *text) Write(p []byte) (n int, err error) {
	*t = append(*t, p...)
	return len(p), nil
}

// unstyled reports the first segment that is neither text nor SGR.
func (t text) unstyled() (segment, bool) {
	for seg := range t.segments() {
		if _, sgr := seg.sgrParams(); seg.isText() || (seg.isEscape && sgr) {
			continue
		}
		return seg, true
	}
	return segment{}, false
}

// segments returns the segments of t, in order.
func (t text) segments() iter.Seq[segment] {
	return func(yield func(segment) bool) {
		for rest := t; len(rest) > 0; {
			seg := rest.scan()
			if !yield(seg) {
				return
			}
			rest = rest[len(seg.text):]
		}
	}
}

// scan parses the first segment of non-empty t.
func (t text) scan() segment {
	c := t[0]
	switch {
	case c == 0x1b:
		return t.escapeSegment()
	case c < 0x20 || c == 0x7f:
		return segment{isControl: true, text: t[:1]}
	case c < utf8.RuneSelf && (len(t) == 1 || t[1] < utf8.RuneSelf):
		return segment{text: t[:1], width: 1}
	}
	r, size := utf8.DecodeRune(t)
	switch {
	case r == utf8.RuneError && size == 1:
		return segment{isInvalid: true, text: t[:1], width: 1}
	case r >= 0x80 && r < 0xa0:
		return segment{isControl: true, text: t[:size]}
	}
	return t.scanCluster(glyph(r), size)
}

// scanCluster collects the runes attached to t's leading glyph.
func (t text) scanCluster(r glyph, size int) segment {
	w, n, prev := r.width(), size, r
	paired := false
	for n < len(t) {
		decoded, nextSize := utf8.DecodeRune(t[n:])
		if decoded == utf8.RuneError && nextSize <= 1 {
			break
		}
		switch next := glyph(decoded); {
		case next == '‍': // ZWJ joins the pictographic character after it
			n += nextSize
			j, joinedSize := utf8.DecodeRune(t[n:])
			if joined := glyph(j); n < len(t) &&
				prev.has(pictGlyph) && joined.has(pictGlyph) {
				w, n, prev = max(w, joined.width()), n+joinedSize, joined
			}
			continue
		case next == '️': // emoji presentation, but not of a plain digit
			if w == 1 && prev >= 0x80 && prev.has(emojiGlyph) {
				w = 2
			}
		case next == '⃣': // keycap
			if prev == '#' || prev == '*' || (prev >= '0' && prev <= '9') {
				w = 2
			}
		case next >= 0x1f3fb && next <= 0x1f3ff: // emoji modifier
			if !prev.has(pictGlyph) {
				return segment{text: t[:n], width: w}
			}
			w = 2
		case next.isRegionalIndicator():
			if paired || !prev.isRegionalIndicator() {
				return segment{text: t[:n], width: w}
			}
			paired, w = true, 2
		case !next.isZeroWidth():
			return segment{text: t[:n], width: w}
		}
		n += nextSize
	}
	return segment{text: t[:n], width: w}
}

// width returns the number of terminal columns the text occupies.
func (t text) width() int {
	w := 0
	for seg := range t.segments() {
		w += seg.width
	}
	return w
}

// truncateVisible keeps t's first line within maxLen columns and appends tailer.
func (t text) truncateVisible(maxLen int, tailer byte) text {
	out := t.beforeNewline().truncateColumns(maxLen - 1)
	if len(out) > 0 && out[len(out)-1] != tailer {
		out = append(out, tailer)
	}
	return out
}

// oneLine keeps the text and SGR of caller content on one row: line breaks
// and tabs become spaces, invalid bytes U+FFFD, and other controls and
// escape sequences are dropped.
func (t text) oneLine() string {
	var out text
	for seg := range t.segments() {
		_, sgr := seg.sgrParams()
		switch {
		case seg.isText(), seg.isEscape && sgr:
			out = append(out, seg.text...)
		case seg.isInvalid:
			out = append(out, "\uFFFD"...)
		case seg.isControl && bytes.ContainsAny(seg.text, "\r\n\t"):
			out = append(out, ' ')
		}
	}
	return out.String()
}

// beforeNewline returns t before its first line break.
func (t text) beforeNewline() text {
	for i, b := range t {
		if b == '\r' || b == '\n' {
			return t[:i]
		}
	}
	return t
}

// truncateColumns fits t into maxLen columns, closing an open hyperlink and
// style when needed.
// It drops controls, incomplete or broken sequences and replaces invalid bytes with U+FFFD.
func (t text) truncateColumns(maxLen int) text {
	if maxLen <= 0 {
		return nil
	}
	cut := t.width() > maxLen
	budget := maxLen
	if cut {
		budget-- // the column of the ellipsis
	}
	var out text
	used := 0
	// styled is set when an SGR sequence may have left a style open.
	styled := false
	// linked is set when the last OSC 8 sequence kept opened a hyperlink.
	linked := false
	for seg := range t.segments() {
		switch {
		case seg.isIncomplete, seg.isBroken, seg.isControl:
			continue // controls move the cursor, so they cannot count as zero columns
		case seg.isEscape:
			if !utf8.Valid(seg.text) {
				continue // a control string may carry any bytes
			}
			if s, ok := seg.sgrStyle(); ok {
				styled = s
			}
			if seg.isHyperlink() {
				linked = true
			} else if seg.endsHyperlink() {
				linked = false
			}
			out = append(out, seg.text...)
			continue
		}
		used += seg.width
		if used > budget {
			break
		}
		if seg.isInvalid {
			out = utf8.AppendRune(out, utf8.RuneError)
		} else {
			out = append(out, seg.text...)
		}
	}
	if cut {
		out = append(out, "…"...)
	}
	if linked {
		out = append(out, "\x1b]8;;\x1b\\"...)
	}
	if styled {
		out = append(out, "\x1b[0m"...)
	}
	return out
}

// escapeSegment parses t's leading ESC sequence as complete, incomplete, or broken.
func (t text) escapeSegment() segment {
	if len(t) < 2 {
		return segment{text: t[:], isIncomplete: true}
	}
	switch c := t[1]; {
	case c == '[':
		return t.csiSegment()
	case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
		return t.controlStringSegment()
	case c >= 0x20 && c <= 0x2f: // nF: intermediates, then a final byte
		for i := 2; i < len(t); i++ {
			switch {
			case t[i] >= 0x30 && t[i] <= 0x7e:
				return segment{text: t[:i+1], isEscape: true}
			case t[i] < 0x20 || t[i] > 0x2f:
				return segment{text: t[:i], isBroken: true}
			}
		}
		return segment{text: t[:], isIncomplete: true}
	case c >= 0x30 && c <= 0x7e:
		return segment{text: t[:2], isEscape: true}
	default:
		return segment{text: t[:1], isBroken: true}
	}
}

// csiSegment parses t's leading ESC [ control sequence.
func (t text) csiSegment() segment {
	for i := 2; i < len(t); i++ {
		switch c := t[i]; {
		case c >= 0x20 && c <= 0x3f: // parameter and intermediate bytes
		case c >= 0x40 && c <= 0x7e: // final byte
			return segment{text: t[:i+1], isEscape: true}
		default:
			return segment{text: t[:i], isBroken: true}
		}
	}
	return segment{text: t[:], isIncomplete: true}
}

// controlStringSegment parses t's leading OSC, DCS, SOS, PM, or APC string.
func (t text) controlStringSegment() segment {
	for i := 2; i < len(t); i++ {
		switch t[i] {
		case 0x07:
			if t[1] == ']' {
				return segment{text: t[:i+1], isEscape: true}
			}
		case 0x18, 0x1a:
			return segment{text: t[:i+1], isBroken: true}
		case '\r', '\n': // a line break always ends the line
			return segment{text: t[:i], isBroken: true}
		case 0x1b:
			if i+1 == len(t) {
				return segment{text: t[:], isIncomplete: true}
			}
			if t[i+1] == '\\' {
				return segment{text: t[:i+2], isEscape: true}
			}
			return segment{text: t[:i], isBroken: true}
		}
	}
	return segment{text: t[:], isIncomplete: true}
}

// partialRuneTail returns t's trailing incomplete UTF-8 rune length.
func (t text) partialRuneTail() int {
	start := len(t) - 1
	for start > 0 && len(t)-start < utf8.UTFMax && !utf8.RuneStart(t[start]) {
		start--
	}
	if start < 0 || utf8.FullRune(t[start:]) {
		return 0
	}
	return len(t) - start
}

// incompleteTail returns a trailing split sequence to hold for the next write.
func (t text) incompleteTail() int {
	if n := t.incompleteSequence(); n > 0 && n <= maxHeldBytes {
		return n
	} else if n > 0 {
		return 0
	}
	return t.partialRuneTail()
}

// incompleteSequence returns the length of an escape sequence cut by the end of t.
func (t text) incompleteSequence() int {
	for seg := range t.segments() {
		if seg.isIncomplete {
			return len(seg.text)
		}
	}
	return 0
}

// stripBroken removes sequences that would swallow following terminal text.
func (t text) stripBroken() text {
	var out text
	kept, at := 0, 0 // t[kept:at] is not copied to out yet
	for seg := range t.segments() {
		if seg.isBroken {
			out = append(out, t[kept:at]...)
			kept = at + len(seg.text)
		}
		at += len(seg.text)
	}
	if kept == 0 {
		return t
	}
	return append(out, t[kept:]...)
}
