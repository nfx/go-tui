// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/assert"
)

func TestTruncateColumns(t *testing.T) {
	for _, tt := range []struct {
		in  string
		out string
	}{
		{"12345\x1b[1;31m6\x1b[0m789", "123…"},
		{"\x1b[1;31m12\x1b[0m\x1b[1;32m345\x1b[0m", "\x1b[1;31m12\x1b[0m\x1b[1;32m3…\x1b[0m"},
		{"1\x1b[1;31m2\x1b[0m345", "1\x1b[1;31m2\x1b[0m3…"},
		{"1\x1b[1;31m23\x1b[0m45", "1\x1b[1;31m23\x1b[0m…"},
		{"1\x1b[1;31m234\x1b[0m5", "1\x1b[1;31m23…\x1b[0m"},
		{"1\x1b[1;31m2\x1b[1;32m3\x1b[0m45\x1b[0m6", "1\x1b[1;31m2\x1b[1;32m3\x1b[0m…"},
		{"1\x1b[1;31m2\x1b[1;32m34\x1b[0m5\x1b[0m6", "1\x1b[1;31m2\x1b[1;32m3…\x1b[0m"},
		{"", ""},
		{"a", "a"},
		{"ab", "ab"},
		{"abc", "abc"},
		{"abcd", "abcd"},
		{"abcdef", "abc…"},
		{"abcde", "abc…"},
		{"åäöxy", "åäö…"},
		{"\x1b[31måäöxy\x1b[0m", "\x1b[31måäö…\x1b[0m"},
	} {
		t.Run(fmt.Sprint(tt), func(t *testing.T) {
			assert.Equal(t, tt.out, string(text(tt.in).truncateColumns(4)))
		})
	}
}

func TestBeforeNewline(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{"no newlines", "no newlines"},
		{"hello\nworld", "hello"},
		{"hello\rworld", "hello"},
		{"hello\r\nworld", "hello"},
		{"\nstart", ""},
		{"\rstart", ""},
		{"", ""},
		{"abc\ndef\nghi", "abc"},
	} {
		t.Run(fmt.Sprint(tt), func(t *testing.T) {
			assert.Equal(t, tt.want, string(text(tt.in).beforeNewline()))
		})
	}
}

func TestWidthUnicode(t *testing.T) {
	//nolint:gosmopolitan // intent is to verify width handling on specific scripts.
	for _, tt := range []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"åäö", 3},
		{"a\u0308", 1},            // combining diaeresis
		{"\x1b[31må\x1b[0m", 1},   // colored single rune
		{"\x1b[31måäö\x1b[0m", 3}, // colored multi-byte runes
		{"界面", 4},                 // wide runes
		{"\x1b[32m界面\x1b[0m", 4},  // wide runes with color
		{"界\u0308", 2},            // wide rune plus combining mark
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, text(tt.in).width())
		})
	}
}

func TestTruncationResetsSGR(t *testing.T) {
	for _, tt := range []struct {
		in, out string
	}{
		{"\x1b[31mabcdef", "\x1b[31mab…\x1b[0m"},
		{"\x1b[1m\x1b[31m\x1b[4mabcdef", "\x1b[1m\x1b[31m\x1b[4mab…\x1b[0m"}, // one reset for many styles
		{"\x1b[31ma\x1b[0mbcdef", "\x1b[31ma\x1b[0mb…"},
		{"\x1b[mabcdef", "\x1b[mab…"},         // empty parameters reset
		{"\x1b[0;00mabcdef", "\x1b[0;00mab…"}, // so do zeros
		{"\x1b[;mabcdef", "\x1b[;mab…"},
		{"\x1b[31ma\x1b[1;0mbcdef", "\x1b[31ma\x1b[1;0mb…\x1b[0m"}, // mixed parameters may style
		{"\x1b[0;1mabcdef", "\x1b[0;1mab…\x1b[0m"},
		{"\x1b[38;5;0mabcdef", "\x1b[38;5;0mab…\x1b[0m"},         // indexed color
		{"\x1b[38;2;0;0;0mabcdef", "\x1b[38;2;0;0;0mab…\x1b[0m"}, // RGB color
		{"\x1b[38:2::0:0:0mabcdef", "\x1b[38:2::0:0:0mab…\x1b[0m"},
		{"\x1b[>4;2mabcdef", "\x1b[>4;2mab…"}, // private sequences are not SGR
		{"\x1b[31m\x1b[?0mabcdef", "\x1b[31m\x1b[?0mab…\x1b[0m"},
		{"\x1b[31m\x1b[0Kabcdef", "\x1b[31m\x1b[0Kab…\x1b[0m"}, // nor are other CSI
		{"\x1b[31mab", "\x1b[31mab\x1b[0m"},                    // closed even if not cut
		{"abc\x1b[31mdef", "ab…"},                              // styles after the cut are dropped
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.out, string(text(tt.in).truncateColumns(3)))
		})
	}
}

func TestTruncateVisible(t *testing.T) {
	for _, tt := range []struct {
		in     string
		maxLen int
		tailer byte
		out    string
	}{
		{"hello world", 6, ' ', "hell… "},
		{"hello", 6, ' ', "hello "},
		{"hello ", 6, ' ', "hell… "},
		{"hello world\nnext", 6, '\n', "hell…\n"},
		{"界界界界", 6, ' ', "界界… "},
		{"界界界界", 5, ' ', "界… "},
		{"abc", 1, ' ', ""},
		{"abc", 0, ' ', ""},
		{"a\xffb", 10, ' ', "a\ufffdb "},
		{"ab\x1b[3", 10, ' ', "ab "},
		{"ab\x1b]0;title", 10, ' ', "ab "},
		{"e\u0301e\u0301e\u0301", 3, ' ', "e\u0301… "},
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.out, string(text(tt.in).truncateVisible(tt.maxLen, tt.tailer)))
		})
	}
}

func withText(flags segment, text []byte, width int) segment {
	flags.text, flags.width = text, width
	return flags
}

func TestScan(t *testing.T) {
	for _, tt := range []struct {
		in    string
		kind  segment // only the flags
		size  int
		width int
	}{
		{"a", segment{}, 1, 1},
		{"ab", segment{}, 1, 1},
		{"\n", segment{isControl: true}, 1, 0},
		{"\t", segment{isControl: true}, 1, 0},
		{"\x7f", segment{isControl: true}, 1, 0},
		{"\u009bx", segment{isControl: true}, 2, 0}, // C1 is not CSI
		{"\x1b", segment{isIncomplete: true}, 1, 0},
		{"\x1bx", segment{isEscape: true}, 2, 0},       // two-byte
		{"\x1b\x1b[0m", segment{isBroken: true}, 1, 0}, // a lone ESC
		{"\x1b界", segment{isBroken: true}, 1, 0},
		{"\x1b[31m", segment{isEscape: true}, 5, 0},
		{"\x1b[?25h", segment{isEscape: true}, 6, 0},
		{"\x1b[31", segment{isIncomplete: true}, 4, 0},
		{"\x1b[31\nx", segment{isBroken: true}, 4, 0}, // malformed, ends before \n
		{"\x1b(B", segment{isEscape: true}, 3, 0},
		{"\x1b(", segment{isIncomplete: true}, 2, 0},
		{"\x1b]0;title\x07x", segment{isEscape: true}, 10, 0},
		{"\x1b]8;;http://a\x1b\\x", segment{isEscape: true}, 15, 0},
		{"\x1b]8;;http://a", segment{isIncomplete: true}, 13, 0},
		{"\x1b]8;;http://a\x1b", segment{isIncomplete: true}, 14, 0},
		{"\x1b]8;;a\x1b[0m", segment{isBroken: true}, 6, 0},   // ESC cancels a string
		{"\x1b]t\nx", segment{isBroken: true}, 3, 0},          // so does a line break
		{"\x1bPdata\x07x", segment{isIncomplete: true}, 8, 0}, // BEL ends only OSC
		{"\xff", segment{isInvalid: true}, 1, 1},
		{"\xffa", segment{isInvalid: true}, 1, 1},
		{"\xe4", segment{isInvalid: true}, 1, 1},
		{"\xe4\xb8", segment{isInvalid: true}, 1, 1},
		{"界", segment{}, 3, 2},
		{"é", segment{}, 2, 1},
		{"e\u0301", segment{}, 3, 1},
		{"e\u0301\u0302x", segment{}, 5, 1},
		{"\u0301", segment{}, 2, 0},        // an orphan mark
		{"1\ufe0f\u20e3", segment{}, 7, 2}, // keycap
		{"❤", segment{}, 3, 1},
		{"❤\ufe0f", segment{}, 6, 2},     // emoji presentation
		{"☝\U0001F3FD", segment{}, 7, 2}, // narrow base with a skin tone
		{"👍🏽", segment{}, 8, 2},
		{"👨\u200d👩\u200d👧", segment{}, 18, 2},
		{"❤\ufe0f\u200d🔥", segment{}, 13, 2},
		{"a\u200d🔥", segment{}, 4, 1}, // ZWJ after a non-emoji
		{"🇺🇦", segment{}, 8, 2},
		{"🇺🇦🇺", segment{}, 8, 2}, // the third indicator starts a new flag
		{"🇺", segment{}, 4, 1},
		{"🏴\U000E0067\U000E0062\U000E007F", segment{}, 16, 2}, // tag sequence
		{"각", segment{}, 3, 2},
		{"\u1100\u1161\u11a8", segment{}, 9, 2}, // L V T jamo
		{"한", segment{}, 3, 2},
		{"ｱ", segment{}, 3, 1}, // halfwidth katakana
		{"Ａ", segment{}, 3, 2}, // fullwidth
		{"\u00ad", segment{}, 2, 0},
	} {
		t.Run(tt.in, func(t *testing.T) {
			got := text(tt.in).scan()
			assert.Equal(t, withText(tt.kind, []byte(tt.in)[:tt.size], tt.width), got)
		})
	}
}

// regression: a segment sequence starts from the beginning every time it is iterated
func TestTokensIterateTwice(t *testing.T) {
	seq := text("a\x1b[31m界").segments()
	var first, second []segment
	for seg := range seq {
		first = append(first, seg)
	}
	for seg := range seq {
		second = append(second, seg)
	}
	assert.Equal(t, []segment{
		{text: []byte("a"), width: 1},
		{isEscape: true, text: []byte("\x1b[31m")},
		{text: []byte("界"), width: 2},
	}, first)
	assert.Equal(t, first, second)
}

func TestIncompleteTail(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 0},
		{"abc\x1b", 1},
		{"abc\x1b[", 2},
		{"abc\x1b[31", 4},
		{"abc\x1b[31m", 0},
		{"\x1b]8;;http://a\x1b", 14},
		{"\x1b]8;;http://a\x1b\\", 0},
		{"\x1b[0m\x1b]0;ti", 6},
		{"ab\xe4", 1},
		{"ab\xe4\xb8", 2},
		{"ab界", 0},
		{"ab\xff", 0},
		{"\x1b]" + strings.Repeat("x", maxHeldBytes), 0}, // never terminated
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, text(tt.in).incompleteTail())
		})
	}
}

func TestClusters(t *testing.T) {
	got := text("ae\u0301界\x1b[0m👍🏽").clusters()
	assert.Equal(t, clusters{
		{0, 1, 1}, {1, 3, 1}, {3, 4, 2}, {4, 8, 0}, {8, 10, 2},
	}, got)
}

func TestWidthEmoji(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int
	}{
		{"👍", 2},
		{"a👍b", 4},
		{"❤\ufe0f x", 4},
		{"\x1b]8;;http://example.com\x07link\x1b]8;;\x07", 4}, // OSC 8 hyperlink
		{"\x1b]0;title\x07", 0},
		{"a\xffb", 3},
		{"abc\xe4", 4}, // a rune cut by the end is invalid
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, text(tt.in).width())
		})
	}
}

// fuzz seeds that cover every kind of segment.
var fuzzSeeds = []string{
	"", "plain", "\x1b[31mred\x1b[0m text", "界面 wide", "e\u0301 combining", "👨\u200d👩\u200d👧 family",
	"🇺🇦 flag", "❤\ufe0f", "1\ufe0f\u20e3", "\x1b]8;;http://a\x07link\x1b]8;;\x07", "\x1b]0;title\x1b\\",
	"\x1b[", "\x1b", "\xff\xfe", "\xe4\xb8", "a\tb\rc\nd", "\x1b(B", "\u1100\u1161\u11a8", "\x9b31m",
}

func FuzzScan(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		text := text(data)
		total, i := 0, 0
		for seg := range text.segments() {
			if len(seg.text) == 0 || &seg.text[0] != &data[i] {
				t.Fatalf("segment %q at %d of %q", seg.text, i, data)
			}
			if seg.width < 0 || seg.width > 2 {
				t.Fatalf("segment width %d", seg.width)
			}
			if !seg.isText() && !seg.isInvalid && seg.width != 0 {
				t.Fatalf("%+v has width %d", seg, seg.width)
			}
			if seg.isText() && !utf8.Valid(seg.text) {
				t.Fatalf("invalid cluster %q", seg.text)
			}
			if seg.isIncomplete && i+len(seg.text) != len(data) {
				t.Fatalf("incomplete segment %q in the middle", seg.text)
			}
			total += seg.width
			i += len(seg.text)
		}
		if i != len(data) {
			t.Fatalf("segments cover %d of %d bytes", i, len(data))
		}
		if text.width() != total {
			t.Fatalf("width %d != %d", text.width(), total)
		}
		if tail := text.incompleteTail(); tail > len(data) {
			t.Fatalf("tail %d of %d", tail, len(data))
		}
	})
}

func FuzzTruncateVisible(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed), uint8(4), false)
		f.Add([]byte(seed), uint8(1), true)
		f.Add([]byte(seed), uint8(20), false)
	}
	f.Fuzz(func(t *testing.T, data []byte, maxLen uint8, newline bool) {
		tailer := byte(' ')
		if newline {
			tailer = '\n'
		}
		out := text(data).truncateVisible(int(maxLen), tailer)
		if !utf8.Valid(out) {
			t.Fatalf("invalid UTF-8 %q from %q", out, data)
		}
		if got := out.width(); got > int(maxLen) {
			t.Fatalf("width %d exceeds %d: %q from %q", got, maxLen, out, data)
		}
		for seg := range out.segments() {
			if seg.isIncomplete || seg.isInvalid || seg.isBroken {
				t.Fatalf("split sequence %q in %q from %q", seg.text, out, data)
			}
		}
		if bytes.ContainsAny(out, "\r") || (!newline && bytes.Contains(out[:max(len(out)-1, 0)], []byte{'\n'})) {
			t.Fatalf("line break in %q from %q", out, data)
		}
	})
}

func FuzzViewportWrap(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed), []byte("tail"), uint8(5))
		f.Add([]byte(seed), []byte("\x1b[0m\u0301"), uint8(2))
	}
	f.Fuzz(func(t *testing.T, first, second []byte, cols uint8) {
		v := &viewport{width: int(cols%40) + 2, height: 100}
		for _, chunk := range [][]byte{first, second} {
			v.appendToLinebuffer(v.joinPartial(chunk))
		}
		for _, line := range v.lines {
			if !bytes.HasSuffix(line, []byte{'\n'}) {
				t.Fatalf("line %q has no line break", line)
			}
			body := text(line[:len(line)-1])
			if w := body.width(); w > v.width && !oneCluster(body) {
				t.Fatalf("width %d exceeds %d: %q", w, v.width, body)
			}
			for seg := range body.segments() {
				if seg.isIncomplete || seg.isBroken {
					t.Fatalf("split sequence %q in %q", seg.text, body)
				}
			}
		}
	})
}

// oneCluster reports whether line holds a single visible segment,
// which is wider than the viewport and cannot be wrapped.
func oneCluster(line text) bool {
	visible := 0
	for seg := range line.segments() {
		if seg.width > 0 {
			visible++
		}
	}
	return visible <= 1
}

func TestStripBroken(t *testing.T) {
	for _, tt := range []struct {
		in, out string
	}{
		{"", ""},
		{"ab\x1b[31mc\x1b]0;t", "ab\x1b[31mc\x1b]0;t"}, // incomplete is kept
		{"\x1b\x1b[0m", "\x1b[0m"},
		{"a\x1b\x1b[0mb\x1b", "a\x1b[0mb\x1b"},
		{"a\x1b[31\nb", "a\nb"},
		{"\x1b]0;t\x1b[0m", "\x1b[0m"},
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.out, string(text(tt.in).stripBroken()))
		})
	}
	clean := []byte("ab\x1b[31mc")
	assert.True(t, &text(clean).stripBroken()[0] == &clean[0])
}
