// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"iter"
	"slices"
	"unicode"
	"unicode/utf8"
)

const (
	// maxHeldBytes limits how much of an unterminated sequence is held back.
	maxHeldBytes = 4096
)

// text is text written to a terminal: UTF-8 with controls and escape sequences.
type text []byte

// String returns t as a string.
func (t text) String() string {
	return string(t)
}

// Write appends p to t, so that templates and formatters render into it.
func (t *text) Write(p []byte) (n int, err error) {
	*t = append(*t, p...)
	return len(p), nil
}

// glyph is a code point, as a terminal shows it.
type glyph rune

// glyphFlags are the Unicode properties of a rune that [glyphRanges] records.
type glyphFlags uint8

const (
	wideGlyph  glyphFlags = 1 << iota // East_Asian_Width W or F
	pictGlyph                         // Extended_Pictographic
	emojiGlyph                        // Emoji
)

//go:generate sh -c "go run glyphs_gen.go | gofmt > glyphs.go"

type glyphRange struct {
	lo, hi rune
	flags  glyphFlags
}

// has reports whether r has all the properties p.
func (r glyph) has(p glyphFlags) bool {
	i, found := slices.BinarySearchFunc(glyphRanges, rune(r), func(c glyphRange, r rune) int {
		switch {
		case r < c.lo:
			return 1
		case r > c.hi:
			return -1
		default:
			return 0
		}
	})
	return found && glyphRanges[i].flags&p == p
}

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
	params, ok := bytes.CutPrefix(t.text, []byte("\x1b["))
	if !ok {
		return false, false
	}
	params, ok = bytes.CutSuffix(params, []byte("m"))
	// private parameters, like in ESC [ > 4 m, are not SGR
	if !ok || len(bytes.Trim(params, "0123456789;:")) > 0 {
		return false, false
	}
	return len(bytes.Trim(params, "0;")) > 0, true
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

// isRegionalIndicator reports whether r participates in a flag pair.
func (r glyph) isRegionalIndicator() bool {
	return r >= 0x1f1e6 && r <= 0x1f1ff
}

// isZeroWidth reports whether r occupies no column by itself.
func (r glyph) isZeroWidth() bool {
	if r < 0x300 {
		return r == 0xad // soft hyphen
	}
	return unicode.In(rune(r), unicode.Mn, unicode.Me, unicode.Mc, unicode.Cf) ||
		(r >= 0x1160 && r <= 0x11ff) || (r >= 0xd7b0 && r <= 0xd7ff)
}

// width returns the number of terminal columns the text occupies.
func (t text) width() int {
	w := 0
	for seg := range t.segments() {
		w += seg.width
	}
	return w
}

// width returns r's standalone terminal width.
func (r glyph) width() int {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	// see unicode combining marks in https://www.unicode.org/reports/tr29/
	case r.isZeroWidth():
		return 0
	case r >= 0x1100 && r.has(wideGlyph):
		return 2
	default:
		return 1
	}
}

// truncateVisible keeps t's first line within maxLen columns and appends tailer.
func (t text) truncateVisible(maxLen int, tailer byte) text {
	out := t.beforeNewline().truncateColumns(maxLen - 1)
	if len(out) > 0 && out[len(out)-1] != tailer {
		out = append(out, tailer)
	}
	return out
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

// cluster is a segment of text, measured in runes and columns.
type cluster struct {
	start, end int // rune offsets: [start, end)
	width      int
}

// clusters splits t into cursor and wrapping boundaries.
type clusters []cluster

func (t text) clusters() clusters {
	cells := make(clusters, 0, len(t))
	offset := 0
	for seg := range t.segments() {
		runes := utf8.RuneCount(seg.text)
		cells = append(cells, cluster{start: offset, end: offset + runes, width: seg.width})
		offset += runes
	}
	return cells
}

// width returns the terminal columns occupied by c.
func (c clusters) width() int {
	w := 0
	for _, cell := range c {
		w += cell.width
	}
	return w
}

// at returns the cluster containing cursor, or len(c) at the end.
func (c clusters) at(cursor int) int {
	for k, cell := range c {
		if cursor < cell.end {
			return k
		}
	}
	return len(c)
}

// runeOffset returns the rune offset at cluster k's start.
func (c clusters) runeOffset(k int) int {
	switch {
	case k < len(c):
		return c[k].start
	case len(c) > 0:
		return c[len(c)-1].end
	default:
		return 0
	}
}
