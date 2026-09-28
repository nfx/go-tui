// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"unicode"
	"unicode/utf8"
)

func isEscapeEnd(r byte) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func isEscapeStart(r byte) bool {
	return r == '\x1b' || r == '\x9b'
}

func width(chunk []byte) int {
	w, escape := 0, false
	for i := 0; i < len(chunk); {
		r, size := utf8.DecodeRune(chunk[i:])
		if escape {
			if isEscapeTerminator(r) {
				escape = false
			}
			i += size

			continue
		}
		if r <= utf8.RuneSelf && isEscapeStart(byte(r)) {
			escape = true
			i += size

			continue
		}
		w += runeWidth(r)
		i += size
	}

	return w
}

func runeWidth(r rune) int {
	switch {
	case r == utf8.RuneError:
		return 0
	case r == '\n' || r == '\r' || r == '\t':
		return 0
	// see unicode combining marks in https://www.unicode.org/reports/tr29/
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc, unicode.Cf):
		return 0
	case isWideRune(r):
		return 2
	default:
		return 1
	}
}

func isEscapeTerminator(r rune) bool {
	return r <= utf8.RuneSelf && isEscapeEnd(byte(r))
}

func isWideRune(r rune) bool {
	for _, rng := range wideRanges {
		if r < rng.lo {
			return false
		}
		if r <= rng.hi {
			return true
		}
	}

	return false
}

// wideRanges covers the common East Asian wide/fullwidth blocks.
// see UAX #11 in https://www.unicode.org/Public/UCD/latest/ucd/EastAsianWidth.txt
var wideRanges = []struct{ lo, hi rune }{
	{0x1100, 0x115F},
	{0x2329, 0x232A},
	{0x2E80, 0x303E},
	{0x3040, 0xA4CF},
	{0xAC00, 0xD7A3},
	{0xF900, 0xFAFF},
	{0xFE10, 0xFE19},
	{0xFE30, 0xFE6F},
	{0xFF00, 0xFF60},
	{0xFFE0, 0xFFE6},
	{0x20000, 0x2FFFD},
	{0x30000, 0x3FFFD},
}

// truncateVisible truncates a chunk to fit within maxLen visible characters.
// It first strips content after any newline character to prevent layout issues.
func truncateVisible(chunk []byte, maxLen int, tailer byte) (out []byte) {
	chunk = truncateAtNewline(chunk)
	out = []byte(truncateAscii(string(chunk), maxLen-1))
	if len(out) > 0 && out[len(out)-1] != tailer {
		out = append(out, tailer)
	}

	return
}

// truncateAtNewline returns chunk up to the first \r or \n.
func truncateAtNewline(chunk []byte) []byte {
	for i, b := range chunk {
		if b == '\r' || b == '\n' {
			return chunk[:i]
		}
	}

	return chunk
}

func truncateAscii(chunk string, maxLen int) (out string) {
	if maxLen <= 0 {
		return ""
	}
	if width([]byte(chunk)) <= maxLen {
		return chunk
	}
	state := truncation{
		input:       []byte(chunk),
		escapeStart: -1,
	}
	for state.offset < len(state.input) {
		if state.escape {
			state.consumeEscape()

			continue
		}
		if state.maybeStartEscape() {
			continue
		}
		if state.consumeRune(maxLen) {
			break
		}
	}

	return state.finish()
}

type truncation struct {
	input, output []byte
	width         int
	escape        bool
	escapeStart   int
	openSGRCount  int
	offset        int
}

// consumeEscape copies an ANSI escape sequence, adjusting SGR stack.
func (s *truncation) consumeEscape() {
	r, size := utf8.DecodeRune(s.input[s.offset:])
	if isEscapeTerminator(r) {
		if r == 'm' {
			s.openSGRCount = s.updateSGRCount(s.offset + size)
		}
		s.escape = false
		s.escapeStart--
	}
	s.output = append(s.output, s.input[s.offset:s.offset+size]...)
	s.offset += size
}

// updateSGRCount tracks open SGR sequences based on a terminating 'm'.
func (s *truncation) updateSGRCount(end int) int {
	if s.isResetSGR(end) {
		if s.openSGRCount > 0 {
			return s.openSGRCount - 1
		}

		return s.openSGRCount
	}

	return s.openSGRCount + 1
}

// isResetSGR reports whether the current escape resets attributes.
func (s *truncation) isResetSGR(end int) bool {
	if s.escapeStart < 0 || end > len(s.input) {
		return false
	}
	segment := s.input[s.escapeStart:end]
	if len(segment) == 0 {
		return false
	}
	segment = s.trimEscapePrefix(segment)
	if len(segment) == 0 {
		return false
	}
	segment = s.trimSuffix(segment, 'm')
	for param := range bytes.SplitSeq(segment, []byte{';'}) {
		if len(param) == 0 {
			continue
		}
		if bytes.Equal(param, []byte("0")) {
			return true
		}
	}

	return false
}

// trimEscapePrefix strips CSI introducer from an escape segment.
func (s *truncation) trimEscapePrefix(segment []byte) []byte {
	if segment[0] == '\x9b' {
		return segment[1:]
	}
	begin := bytes.IndexByte(segment, '[')
	if begin >= 0 {
		return segment[begin+1:]
	}

	return nil
}

// trimSuffix drops a trailing SGR terminator.
func (s *truncation) trimSuffix(segment []byte, suffix byte) []byte {
	if len(segment) == 0 {
		return segment
	}
	if segment[len(segment)-1] == suffix {
		return segment[:len(segment)-1]
	}

	return segment
}

// maybeStartEscape begins tracking an ANSI escape sequence.
func (s *truncation) maybeStartEscape() bool {
	r, size := utf8.DecodeRune(s.input[s.offset:])
	if r > utf8.RuneSelf || !isEscapeStart(byte(r)) {
		return false
	}
	s.escape = true
	s.escapeStart = s.offset
	s.output = append(s.output, s.input[s.offset:s.offset+size]...)
	s.offset += size

	return true
}

// consumeRune appends a printable rune unless it would exceed maxLen.
func (s *truncation) consumeRune(maxLen int) bool {
	r, size := utf8.DecodeRune(s.input[s.offset:])
	nextWidth := s.width + runeWidth(r)
	if nextWidth > maxLen-1 {
		return true
	}
	s.width = nextWidth
	s.output = append(s.output, s.input[s.offset:s.offset+size]...)
	s.offset += size

	return false
}

// finish finalizes output with ellipsis and pending SGR resets.
func (s *truncation) finish() string {
	if len(s.output) == 0 {
		return "…"
	}
	s.output = append(s.output, []byte("…")...)
	for range s.openSGRCount {
		s.output = append(s.output, '\x1b', '[', '0', 'm')
	}

	return string(s.output)
}
