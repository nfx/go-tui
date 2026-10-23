// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package ansi_test

import (
	"bytes"
	"testing"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/tcommon"
)

// fuzz seeds that cover every kind of segment.
func FuzzScan(f *testing.F) {
	for _, seed := range tcommon.TextSeeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		text := ansi.Text(data)
		total, i := 0, 0
		for seg := range text.Segments() {
			if len(seg.Text) == 0 || &seg.Text[0] != &data[i] {
				t.Fatalf("segment %q at %d of %q", seg.Text, i, data)
			}
			if seg.Width < 0 || seg.Width > 2 {
				t.Fatalf("segment width %d", seg.Width)
			}
			if !seg.IsText() && !seg.IsInvalid && seg.Width != 0 {
				t.Fatalf("%+v has width %d", seg, seg.Width)
			}
			if seg.IsText() && !utf8.Valid(seg.Text) {
				t.Fatalf("invalid cluster %q", seg.Text)
			}
			if seg.IsIncomplete && i+len(seg.Text) != len(data) {
				t.Fatalf("incomplete segment %q in the middle", seg.Text)
			}
			total += seg.Width
			i += len(seg.Text)
		}
		if i != len(data) {
			t.Fatalf("segments cover %d of %d bytes", i, len(data))
		}
		if text.Width() != total {
			t.Fatalf("width %d != %d", text.Width(), total)
		}
		if tail := text.IncompleteTail(); tail > len(data) {
			t.Fatalf("tail %d of %d", tail, len(data))
		}
	})
}

func FuzzTruncateVisible(f *testing.F) {
	for _, seed := range tcommon.TextSeeds {
		f.Add([]byte(seed), uint8(4), false)
		f.Add([]byte(seed), uint8(1), true)
		f.Add([]byte(seed), uint8(20), false)
	}
	f.Fuzz(func(t *testing.T, data []byte, maxLen uint8, newline bool) {
		tailer := byte(' ')
		if newline {
			tailer = '\n'
		}
		out := ansi.Text(data).TruncateVisible(int(maxLen), tailer)
		if !utf8.Valid(out) {
			t.Fatalf("invalid UTF-8 %q from %q", out, data)
		}
		if got := out.Width(); got > int(maxLen) {
			t.Fatalf("width %d exceeds %d: %q from %q", got, maxLen, out, data)
		}
		for seg := range out.Segments() {
			if seg.IsIncomplete || seg.IsInvalid || seg.IsBroken {
				t.Fatalf("split sequence %q in %q from %q", seg.Text, out, data)
			}
		}
		if bytes.ContainsAny(out, "\r") || (!newline && bytes.Contains(out[:max(len(out)-1, 0)], []byte{'\n'})) {
			t.Fatalf("line break in %q from %q", out, data)
		}
	})
}
