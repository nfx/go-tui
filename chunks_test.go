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

func TestTruncationTrimEscapePrefix(t *testing.T) {
	s := &truncation{}
	if got := s.trimEscapePrefix([]byte("\x9b0m")); string(got) != "0m" {
		t.Fatalf("unexpected prefix trim %q", string(got))
	}
	if got := s.trimEscapePrefix([]byte("\x1b[31m")); string(got) != "31m" {
		t.Fatalf("unexpected prefix trim %q", string(got))
	}
	if got := s.trimEscapePrefix([]byte("nope")); got != nil {
		t.Fatalf("expected nil")
	}
}

func TestTruncationTrimSuffix(t *testing.T) {
	s := &truncation{}
	if got := s.trimSuffix([]byte("31m"), 'm'); string(got) != "31" {
		t.Fatalf("unexpected suffix trim %q", string(got))
	}
	if got := s.trimSuffix([]byte("31"), 'm'); string(got) != "31" {
		t.Fatalf("unexpected suffix trim %q", string(got))
	}
}

func TestTruncationIsResetSGR(t *testing.T) {
	s := &truncation{
		input:       []byte("\x1b[0m"),
		escapeStart: 0,
	}
	if !s.isResetSGR(len(s.input)) {
		t.Fatalf("expected reset")
	}
	s.input = []byte("\x1b[31m")
	if s.isResetSGR(len(s.input)) {
		t.Fatalf("expected non-reset")
	}
}

func TestTruncationIsResetSGRVariants(t *testing.T) {
	cases := []struct {
		name        string
		input       []byte
		escapeStart int
		end         int
		want        bool
	}{
		{name: "negative escape", input: []byte("\x1b[0m"), escapeStart: -1, end: 4, want: false},
		{name: "end past len", input: []byte("\x1b[0m"), escapeStart: 0, end: 10, want: false},
		{name: "empty params", input: []byte("\x1b[m"), escapeStart: 0, end: 3, want: false},
		{name: "mixed params", input: []byte("\x1b[1;0m"), escapeStart: 0, end: 6, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &truncation{
				input:       tc.input,
				escapeStart: tc.escapeStart,
			}
			if got := s.isResetSGR(tc.end); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestTruncationUpdateSGRCount(t *testing.T) {
	s := &truncation{
		input:        []byte("\x1b[0m"),
		escapeStart:  0,
		openSGRCount: 1,
	}
	if got := s.updateSGRCount(len(s.input)); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	s.openSGRCount = 0
	if got := s.updateSGRCount(len(s.input)); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	s.input = []byte("\x1b[31m")
	if got := s.updateSGRCount(len(s.input)); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}
