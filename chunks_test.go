// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestTruncateASCII(t *testing.T) {
	for _, tt := range []struct {
		in  string
		out string
	}{
		{"12345\x1b[1;31m6\x1b[0m789", "123…"},
		{"\x1b[1;31m12\x1b[0m\x1b[1;32m345\x1b[0m", "\x1b[1;31m12\x1b[0m\x1b[1;32m3…\x1b[0m"},
		{"1\x1b[1;31m2\x1b[0m345", "1\x1b[1;31m2\x1b[0m3…"},
		{"1\x1b[1;31m23\x1b[0m45", "1\x1b[1;31m23\x1b[0m…"},
		{"1\x1b[1;31m234\x1b[0m5", "1\x1b[1;31m23…\x1b[0m"},
		{"1\x1b[1;31m2\x1b[1;32m3\x1b[0m45\x1b[0m6", "1\x1b[1;31m2\x1b[1;32m3\x1b[0m…\x1b[0m"},
		{"1\x1b[1;31m2\x1b[1;32m34\x1b[0m5\x1b[0m6", "1\x1b[1;31m2\x1b[1;32m3…\x1b[0m\x1b[0m"},
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
			assert.Equal(t, tt.out, truncateAscii(tt.in, 4))
		})
	}
}

func TestTruncateAtNewline(t *testing.T) {
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
			assert.Equal(t, tt.want, string(truncateAtNewline([]byte(tt.in))))
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
			assert.Equal(t, tt.want, width([]byte(tt.in)))
		})
	}
}
