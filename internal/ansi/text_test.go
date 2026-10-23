// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package ansi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestTextWrite(t *testing.T) {
	var b Text
	n, err := fmt.Fprintf(&b, "a%dc", 1)
	assert.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, "a1c", b.String())
}

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
			assert.Equal(t, tt.out, string(Text(tt.in).TruncateColumns(4)))
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
			assert.Equal(t, tt.want, string(Text(tt.in).BeforeNewline()))
		})
	}
}

func TestWidthUnicode(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"åäö", 3},
		{"a\u0308", 1},                     // combining diaeresis
		{"\x1b[31må\x1b[0m", 1},            // colored single rune
		{"\x1b[31måäö\x1b[0m", 3},          // colored multi-byte runes
		{"\u754c\u9762", 4},                // wide runes
		{"\x1b[32m\u754c\u9762\x1b[0m", 4}, // wide runes with color
		{"\u754c\u0308", 2},                // wide rune plus combining mark
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, Text(tt.in).Width())
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
			assert.Equal(t, tt.out, string(Text(tt.in).TruncateColumns(3)))
		})
	}
}

func TestTruncationClosesHyperlink(t *testing.T) {
	for _, tt := range []struct {
		in, out string
	}{
		{"\x1b]8;;http://a\x07link text\x1b]8;;\x07", "\x1b]8;;http://a\x07li…\x1b]8;;\x1b\\"},
		{"\x1b]8;id=1;http://a\x1b\\link text", "\x1b]8;id=1;http://a\x1b\\li…\x1b]8;;\x1b\\"},
		{"\x1b]8;;http://a\x07a\x1b]8;;\x07bcdef", "\x1b]8;;http://a\x07a\x1b]8;;\x07b…"}, // already closed
		{"\x1b[31m\x1b]8;;http://a\x07abcdef", "\x1b[31m\x1b]8;;http://a\x07ab…\x1b]8;;\x1b\\\x1b[0m"},
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.out, string(Text(tt.in).TruncateColumns(3)))
		})
	}
}

func TestTruncationDropsControls(t *testing.T) {
	for _, tt := range []struct {
		in, out string
	}{
		{"a\tb", "ab"},
		{"\bab\x7fcdef", "ab…"},
		{"a\u0085b", "ab"},
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.out, string(Text(tt.in).TruncateColumns(3)))
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
		{"\u754c\u754c\u754c\u754c", 6, ' ', "\u754c\u754c… "},
		{"\u754c\u754c\u754c\u754c", 5, ' ', "\u754c… "},
		{"abc", 1, ' ', ""},
		{"abc", 0, ' ', ""},
		{"a\xffb", 10, ' ', "a\ufffdb "},
		{"ab\x1b[3", 10, ' ', "ab "},
		{"ab\x1b]0;title", 10, ' ', "ab "},
		{"e\u0301e\u0301e\u0301", 3, ' ', "e\u0301… "},
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.out, string(Text(tt.in).TruncateVisible(tt.maxLen, tt.tailer)))
		})
	}
}

func withText(flags Segment, text []byte, width int) Segment {
	flags.Text, flags.Width = text, width
	return flags
}

func TestScan(t *testing.T) {
	for _, tt := range []struct {
		in    string
		kind  Segment // only the flags
		size  int
		width int
	}{
		{"a", Segment{}, 1, 1},
		{"ab", Segment{}, 1, 1},
		{"\n", Segment{IsControl: true}, 1, 0},
		{"\t", Segment{IsControl: true}, 1, 0},
		{"\x7f", Segment{IsControl: true}, 1, 0},
		{"\u009bx", Segment{IsControl: true}, 2, 0}, // C1 is not CSI
		{"\x1b", Segment{IsIncomplete: true}, 1, 0},
		{"\x1bx", Segment{IsEscape: true}, 2, 0},       // two-byte
		{"\x1b\x1b[0m", Segment{IsBroken: true}, 1, 0}, // a lone ESC
		{"\x1b\u754c", Segment{IsBroken: true}, 1, 0},
		{"\x1b[31m", Segment{IsEscape: true}, 5, 0},
		{"\x1b[?25h", Segment{IsEscape: true}, 6, 0},
		{"\x1b[31", Segment{IsIncomplete: true}, 4, 0},
		{"\x1b[31\nx", Segment{IsBroken: true}, 4, 0}, // malformed, ends before \n
		{"\x1b(B", Segment{IsEscape: true}, 3, 0},
		{"\x1b(", Segment{IsIncomplete: true}, 2, 0},
		{"\x1b]0;title\x07x", Segment{IsEscape: true}, 10, 0},
		{"\x1b]8;;http://a\x1b\\x", Segment{IsEscape: true}, 15, 0},
		{"\x1b]8;;http://a", Segment{IsIncomplete: true}, 13, 0},
		{"\x1b]8;;http://a\x1b", Segment{IsIncomplete: true}, 14, 0},
		{"\x1b]8;;a\x1b[0m", Segment{IsBroken: true}, 6, 0},   // ESC cancels a string
		{"\x1b]t\nx", Segment{IsBroken: true}, 3, 0},          // so does a line break
		{"\x1bPdata\x07x", Segment{IsIncomplete: true}, 8, 0}, // BEL ends only OSC
		{"\xff", Segment{IsInvalid: true}, 1, 1},
		{"\xffa", Segment{IsInvalid: true}, 1, 1},
		{"\xe4", Segment{IsInvalid: true}, 1, 1},
		{"\xe4\xb8", Segment{IsInvalid: true}, 1, 1},
		{"\u754c", Segment{}, 3, 2},
		{"é", Segment{}, 2, 1},
		{"e\u0301", Segment{}, 3, 1},
		{"e\u0301\u0302x", Segment{}, 5, 1},
		{"\u0301", Segment{}, 2, 0},        // an orphan mark
		{"1\ufe0f\u20e3", Segment{}, 7, 2}, // keycap
		{"❤", Segment{}, 3, 1},
		{"❤\ufe0f", Segment{}, 6, 2},     // emoji presentation
		{"☝\U0001F3FD", Segment{}, 7, 2}, // narrow base with a skin tone
		{"👍🏽", Segment{}, 8, 2},
		{"👨\u200d👩\u200d👧", Segment{}, 18, 2},
		{"❤\ufe0f\u200d🔥", Segment{}, 13, 2},
		{"a\u200d🔥", Segment{}, 4, 1}, // ZWJ after a non-emoji
		{"🇺🇦", Segment{}, 8, 2},
		{"🇺🇦🇺", Segment{}, 8, 2}, // the third indicator starts a new flag
		{"🇺", Segment{}, 4, 1},
		{"🏴\U000E0067\U000E0062\U000E007F", Segment{}, 16, 2}, // tag sequence
		{"각", Segment{}, 3, 2},
		{"\u1100\u1161\u11a8", Segment{}, 9, 2}, // L V T jamo
		{"한", Segment{}, 3, 2},
		{"ｱ", Segment{}, 3, 1}, // halfwidth katakana
		{"Ａ", Segment{}, 3, 2}, // fullwidth
		{"\u00ad", Segment{}, 2, 0},
	} {
		t.Run(tt.in, func(t *testing.T) {
			got := Text(tt.in).Scan()
			assert.Equal(t, withText(tt.kind, []byte(tt.in)[:tt.size], tt.width), got)
		})
	}
}

// regression: a segment sequence starts from the beginning every time it is iterated
func TestTokensIterateTwice(t *testing.T) {
	seq := Text("a\x1b[31m\u754c").Segments()
	var first, second []Segment
	for seg := range seq {
		first = append(first, seg)
	}
	for seg := range seq {
		second = append(second, seg)
	}
	assert.Equal(t, []Segment{
		{Text: []byte("a"), Width: 1},
		{IsEscape: true, Text: []byte("\x1b[31m")},
		{Text: []byte("\u754c"), Width: 2},
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
		{"ab\u754c", 0},
		{"ab\xff", 0},
		{"\x1b]" + strings.Repeat("x", MaxHeldBytes), 0}, // never terminated
	} {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, Text(tt.in).IncompleteTail())
		})
	}
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
			assert.Equal(t, tt.want, Text(tt.in).Width())
		})
	}
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
			assert.Equal(t, tt.out, string(Text(tt.in).StripBroken()))
		})
	}
	clean := []byte("ab\x1b[31mc")
	assert.True(t, &Text(clean).StripBroken()[0] == &clean[0])
}
