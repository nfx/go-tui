// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"iter"
	"unicode/utf8"
)

const (
	// MaxHeldBytes limits how much of an unterminated sequence is held back.
	MaxHeldBytes = 4096
)

// Text is Text written to a terminal: UTF-8 with controls and escape sequences.
type Text []byte //nolint:recvcheck // Write appends in place, the rest only read

// String returns t as a string.
func (t Text) String() string {
	return string(t)
}

// Write appends p to t, so that templates and formatters render into it.
func (t *Text) Write(p []byte) (n int, err error) {
	*t = append(*t, p...)
	return len(p), nil
}

// Unstyled reports the first segment that is neither text nor SGR.
func (t Text) Unstyled() (Segment, bool) {
	for seg := range t.Segments() {
		if _, sgr := seg.SGRParams(); seg.IsText() || (seg.IsEscape && sgr) {
			continue
		}
		return seg, true
	}
	return Segment{}, false
}

// Segments returns the Segments of t, in order.
func (t Text) Segments() iter.Seq[Segment] {
	return func(yield func(Segment) bool) {
		for rest := t; len(rest) > 0; {
			seg := rest.Scan()
			if !yield(seg) {
				return
			}
			rest = rest[len(seg.Text):]
		}
	}
}

// Scan parses the first segment of non-empty t.
func (t Text) Scan() Segment {
	c := t[0]
	switch {
	case c == 0x1b:
		return t.escapeSegment()
	case c < 0x20 || c == 0x7f:
		return Segment{IsControl: true, Text: t[:1]}
	case c < utf8.RuneSelf && (len(t) == 1 || t[1] < utf8.RuneSelf):
		return Segment{Text: t[:1], Width: 1}
	}
	r, size := utf8.DecodeRune(t)
	switch {
	case r == utf8.RuneError && size == 1:
		return Segment{IsInvalid: true, Text: t[:1], Width: 1}
	case r >= 0x80 && r < 0xa0:
		return Segment{IsControl: true, Text: t[:size]}
	}
	return t.scanCluster(glyph(r), size)
}

// scanCluster collects the runes attached to t's leading glyph.
func (t Text) scanCluster(r glyph, size int) Segment {
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
				return Segment{Text: t[:n], Width: w}
			}
			w = 2
		case next.isRegionalIndicator():
			if paired || !prev.isRegionalIndicator() {
				return Segment{Text: t[:n], Width: w}
			}
			paired, w = true, 2
		case !next.isZeroWidth():
			return Segment{Text: t[:n], Width: w}
		}
		n += nextSize
	}
	return Segment{Text: t[:n], Width: w}
}

// Width returns the number of terminal columns the text occupies.
func (t Text) Width() int {
	w := 0
	for seg := range t.Segments() {
		w += seg.Width
	}
	return w
}

// TruncateVisible keeps t's first line within maxLen columns and appends tailer.
func (t Text) TruncateVisible(maxLen int, tailer byte) Text {
	out := t.BeforeNewline().TruncateColumns(maxLen - 1)
	if len(out) > 0 && out[len(out)-1] != tailer {
		out = append(out, tailer)
	}
	return out
}

// OneLine keeps the text and SGR of caller content on one row: line breaks
// and tabs become spaces, invalid bytes U+FFFD, and other controls and
// escape sequences are dropped.
func (t Text) OneLine() string {
	var out Text
	for seg := range t.Segments() {
		_, sgr := seg.SGRParams()
		switch {
		case seg.IsText(), seg.IsEscape && sgr:
			out = append(out, seg.Text...)
		case seg.IsInvalid:
			out = append(out, "\uFFFD"...)
		case seg.IsControl && bytes.ContainsAny(seg.Text, "\r\n\t"):
			out = append(out, ' ')
		}
	}
	return out.String()
}

// BeforeNewline returns t before its first line break.
func (t Text) BeforeNewline() Text {
	for i, b := range t {
		if b == '\r' || b == '\n' {
			return t[:i]
		}
	}
	return t
}

// TruncateColumns fits t into maxLen columns, closing an open hyperlink and
// style when needed.
// It drops controls, incomplete or broken sequences and replaces invalid bytes with U+FFFD.
func (t Text) TruncateColumns(maxLen int) Text {
	if maxLen <= 0 {
		return nil
	}
	cut := t.Width() > maxLen
	budget := maxLen
	if cut {
		budget-- // the column of the ellipsis
	}
	var out Text
	used := 0
	// styled is set when an SGR sequence may have left a style open.
	styled := false
	// linked is set when the last OSC 8 sequence kept opened a hyperlink.
	linked := false
	for seg := range t.Segments() {
		switch {
		case seg.IsIncomplete, seg.IsBroken, seg.IsControl:
			continue // controls move the cursor, so they cannot count as zero columns
		case seg.IsEscape:
			if !utf8.Valid(seg.Text) {
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
			out = append(out, seg.Text...)
			continue
		}
		used += seg.Width
		if used > budget {
			break
		}
		if seg.IsInvalid {
			out = utf8.AppendRune(out, utf8.RuneError)
		} else {
			out = append(out, seg.Text...)
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
func (t Text) escapeSegment() Segment {
	if len(t) < 2 {
		return Segment{Text: t[:], IsIncomplete: true}
	}
	switch c := t[1]; {
	case c == '[':
		return t.CSISegment()
	case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
		return t.controlStringSegment()
	case c >= 0x20 && c <= 0x2f: // nF: intermediates, then a final byte
		for i := 2; i < len(t); i++ {
			switch {
			case t[i] >= 0x30 && t[i] <= 0x7e:
				return Segment{Text: t[:i+1], IsEscape: true}
			case t[i] < 0x20 || t[i] > 0x2f:
				return Segment{Text: t[:i], IsBroken: true}
			}
		}
		return Segment{Text: t[:], IsIncomplete: true}
	case c >= 0x30 && c <= 0x7e:
		return Segment{Text: t[:2], IsEscape: true}
	default:
		return Segment{Text: t[:1], IsBroken: true}
	}
}

// CSISegment parses t's leading ESC [ control sequence.
func (t Text) CSISegment() Segment {
	for i := 2; i < len(t); i++ {
		switch c := t[i]; {
		case c >= 0x20 && c <= 0x3f: // parameter and intermediate bytes
		case c >= 0x40 && c <= 0x7e: // final byte
			return Segment{Text: t[:i+1], IsEscape: true}
		default:
			return Segment{Text: t[:i], IsBroken: true}
		}
	}
	return Segment{Text: t[:], IsIncomplete: true}
}

// controlStringSegment parses t's leading OSC, DCS, SOS, PM, or APC string.
func (t Text) controlStringSegment() Segment {
	for i := 2; i < len(t); i++ {
		switch t[i] {
		case 0x07:
			if t[1] == ']' {
				return Segment{Text: t[:i+1], IsEscape: true}
			}
		case 0x18, 0x1a:
			return Segment{Text: t[:i+1], IsBroken: true}
		case '\r', '\n': // a line break always ends the line
			return Segment{Text: t[:i], IsBroken: true}
		case 0x1b:
			if i+1 == len(t) {
				return Segment{Text: t[:], IsIncomplete: true}
			}
			if t[i+1] == '\\' {
				return Segment{Text: t[:i+2], IsEscape: true}
			}
			return Segment{Text: t[:i], IsBroken: true}
		}
	}
	return Segment{Text: t[:], IsIncomplete: true}
}

// PartialRuneTail returns t's trailing incomplete UTF-8 rune length.
func (t Text) PartialRuneTail() int {
	start := len(t) - 1
	for start > 0 && len(t)-start < utf8.UTFMax && !utf8.RuneStart(t[start]) {
		start--
	}
	if start < 0 || utf8.FullRune(t[start:]) {
		return 0
	}
	return len(t) - start
}

// IncompleteTail returns a trailing split sequence to hold for the next write.
func (t Text) IncompleteTail() int {
	if n := t.IncompleteSequence(); n > 0 && n <= MaxHeldBytes {
		return n
	} else if n > 0 {
		return 0
	}
	return t.PartialRuneTail()
}

// IncompleteSequence returns the length of an escape sequence cut by the end of t.
func (t Text) IncompleteSequence() int {
	for seg := range t.Segments() {
		if seg.IsIncomplete {
			return len(seg.Text)
		}
	}
	return 0
}

// StripBroken removes sequences that would swallow following terminal text.
func (t Text) StripBroken() Text {
	var out Text
	kept, at := 0, 0 // t[kept:at] is not copied to out yet
	for seg := range t.Segments() {
		if seg.IsBroken {
			out = append(out, t[kept:at]...)
			kept = at + len(seg.Text)
		}
		at += len(seg.Text)
	}
	if kept == 0 {
		return t
	}
	return append(out, t[kept:]...)
}
