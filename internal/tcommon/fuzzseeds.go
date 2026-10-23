// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tcommon

// TextSeeds are fuzz seeds that cover plain text, SGR, wide and combining
// runes, hyperlinks and broken sequences.
var TextSeeds = []string{
	"", "plain", "\x1b[31mred\x1b[0m text", "\u754c\u9762 wide", "e\u0301 combining", "👨\u200d👩\u200d👧 family",
	"🇺🇦 flag", "❤\ufe0f", "1\ufe0f\u20e3", "\x1b]8;;http://a\x07link\x1b]8;;\x07", "\x1b]0;title\x1b\\",
	"\x1b[", "\x1b", "\xff\xfe", "\xe4\xb8", "a\tb\rc\nd", "\x1b(B", "\u1100\u1161\u11a8", "\x9b31m",
}
