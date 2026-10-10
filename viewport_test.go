// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/assert"
)

func TestViewport(t *testing.T) {
	for _, tt := range []struct {
		in  string
		out []string
	}{
		{ // explicit NL across lines
			in: "\x1b[1;31m12345\n67890\x1b[0m",
			out: []string{
				"\x1b[1;31m12345     \n",
				"67890\x1b[0m     \n",
			},
		},
		{ // only one line
			in: "this \x1b[1;31mline\x1b[0m has escape sequences.",
			out: []string{
				"this \x1b[1;31mline\x1b[0m \n",
				"has escape\n",
				" sequences\n",
				".         \n",
			},
		},
		{ // across lines
			in: "this \x1b[1;31mline has\x1b[0m escape sequences.",
			out: []string{
				"this \x1b[1;31mline \n",
				"has\x1b[0m escape\n",
				" sequences\n",
				".         \n",
			},
		},
		{
			in: `this line is
without any escaping characters.`,
			out: []string{
				"this line \n",
				"is        \n",
				"without an\n",
				"y escaping\n",
				" character\n",
				"s.        \n",
			},
		},
	} {
		t.Run(fmt.Sprint(tt), func(t *testing.T) {
			v := &viewport{
				width:  10,
				height: 10,
			}
			v.appendToLinebuffer([]byte(tt.in))
			var lines []string
			for _, line := range v.lines {
				lines = append(lines, string(line))
			}
			assert.Equal(t, tt.out, lines)
		})
	}
}

func TestViewportLinkedList(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged)
	v := initViewport(ctx, notify, 10, 4)
	v.lines = [][]byte{[]byte("a\n"), []byte("b\n")}
	v.next = initViewport(ctx, notify, 10, 2)
	v.next.lines = [][]byte{[]byte("c\n"), []byte("d\n")}
	assert.Equal(t, 4, v.numLines())

	var buf bytes.Buffer
	_, err := v.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\ra\n\rb\n\rc\n\rd\n", buf.String())
}

func TestWriteToRotated(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged)
	v := initViewport(ctx, notify, 10, 5)
	v.fixedHeight = true
	v.lastLines = 2
	v.lines = [][]byte{[]byte("a\n"), []byte("b\n"), []byte("aa\n"), []byte("bb\n")}
	v.next = initViewport(ctx, notify, 10, 5)
	v.next.lines = [][]byte{[]byte("c\n"), []byte("d\n"), []byte("e\n"), []byte("f\n"), []byte("g\n")}
	var buf bytes.Buffer
	_, err := v.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\raa\n\rbb\n\re\n\rf\n\rg\n", buf.String())
}

func TestViewportWrite(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged, 10)
	v := initViewport(ctx, notify, 10, 5)

	n, err := v.Write([]byte("hello world"))
	assert.NoError(t, err)
	assert.Equal(t, 11, n)

	// Wait for the write to be processed
	<-notify

	var buf bytes.Buffer
	_, err = v.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\rhello worl\n\rd         \n", buf.String())
}

func TestViewportWriteByte(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged, 10)
	v := initViewport(ctx, notify, 5, 5)

	err := v.WriteByte('a')
	assert.NoError(t, err)

	// Wait for the write to be processed
	<-notify

	var buf bytes.Buffer
	_, err = v.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\ra    \n", buf.String())
}

func TestViewportCombinedHeight(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged, 10)
	v1 := initViewport(ctx, notify, 10, 3)
	v2 := initViewport(ctx, notify, 10, 4)
	v3 := initViewport(ctx, notify, 10, 2)

	v1.next = v2
	v2.next = v3

	assert.Equal(t, 9, v1.combinedHeight())
	assert.Equal(t, 6, v2.combinedHeight())
	assert.Equal(t, 2, v3.combinedHeight())
}

func TestViewportFixedHeight(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged, 10)
	v := initViewport(ctx, notify, 10, 3)
	v.fixedHeight = true
	v.lastLines = 2
	v.lines = [][]byte{[]byte("line1\n"), []byte("line2\n"), []byte("line3\n"), []byte("line4\n")}

	var buf bytes.Buffer
	_, err := v.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\rline3\n\rline4\n", buf.String())
}

func TestViewportTrimLinesExceedsHeight(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged, 10)
	v := initViewport(ctx, notify, 10, 2)
	v.lines = [][]byte{[]byte("line1\n"), []byte("line2\n"), []byte("line3\n"), []byte("line4\n")}

	var buf bytes.Buffer
	_, err := v.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\rline3\n\rline4\n", buf.String())
}

func TestViewportAddLine(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged)
	v := initViewport(ctx, notify, 10, 5)

	chunk := []byte("hello world")
	lo, addedLines := v.addLine(chunk, 0, 5, 0)

	assert.Equal(t, 5, lo)
	assert.Equal(t, 1, addedLines)
	assert.Equal(t, 1, len(v.lines))
	assert.Equal(t, "hello\n", string(v.lines[0]))
}

func TestViewportPadded(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged)
	v := initViewport(ctx, notify, 10, 5)

	chunk := []byte("hello")
	lo, mid := v.padded(chunk, 0, 5)

	assert.Equal(t, 6, lo)
	assert.Equal(t, 6, mid)
	assert.Equal(t, 1, len(v.lines))
	assert.Equal(t, "hello     \n", string(v.lines[0]))
}

func TestViewportPaddedFixedHeightDoesNotAppendSpaces(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged)
	v := initViewport(ctx, notify, 10, 5)
	v.fixedHeight = true

	chunk := []byte("hello\x1b[3D")
	lo, mid := v.padded(chunk, 0, len(chunk))

	assert.Equal(t, len(chunk)+1, lo)
	assert.Equal(t, len(chunk)+1, mid)
	assert.Equal(t, 1, len(v.lines))
	assert.Equal(t, "hello\x1b[3D\n", string(v.lines[0]))
}

func TestViewportSkipsCarriageReturns(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged, 10)
	v := initViewport(ctx, notify, 10, 5)

	n, err := v.Write([]byte("\rhello\n\r"))
	assert.NoError(t, err)
	assert.Equal(t, 8, n)
	<-notify

	var buf bytes.Buffer
	_, err = v.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\rhello     \n", buf.String())
}

func TestViewportWriteToWithBudgetExceeded(t *testing.T) {
	ctx := t.Context()
	notify := make(chan viewportChanged, 10)
	v1 := initViewport(ctx, notify, 10, 2)
	v2 := initViewport(ctx, notify, 10, 5)
	v1.next = v2

	v1.lines = [][]byte{[]byte("line1\n"), []byte("line2\n")}
	v2.lines = [][]byte{[]byte("line3\n"), []byte("line4\n"), []byte("line5\n")}

	var buf bytes.Buffer
	_, err := v1.WriteTo(&buf)
	assert.NoError(t, err)
	assert.Equal(t, "\rline1\n\rline2\n", buf.String())
}

func TestViewportContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	notify := make(chan viewportChanged)
	v := initViewport(ctx, notify, 10, 5)

	cancel()

	_, err := v.Write([]byte("test"))
	assert.Equal(t, io.EOF, err)

	var buf bytes.Buffer
	_, err = v.WriteTo(&buf)
	assert.Equal(t, io.EOF, err)
}

func TestViewportWrapsByTerminalColumns(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		out  []string
	}{
		{"two-byte runes fit", "ééé", []string{"ééé\n"}},
		{"wide runes fill the row", "界界", []string{"界界\n"}},
		{"wide rune wraps whole", "a界界", []string{"a界\n", "界\n"}},
		{"third wide rune wraps", "界界界", []string{"界界\n", "界\n"}},
		{"multibyte at boundary", "abcdé", []string{"abcd\n", "é\n"}},
		{"combining mark stays", "abcdéf", []string{"abcd\n", "éf\n"}},
		{"exact width then newline", "abcd\nef", []string{"abcd\n", "ef\n"}},
		{"escape at boundary stays", "abcd\x1b[0me", []string{"abcd\x1b[0m\n", "e\n"}},
		{"escape is never split", "ab\x1b[1;31mcdé", []string{"ab\x1b[1;31mcd\n", "é\n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := &viewport{width: 4, height: 10, fixedHeight: true}
			v.appendToLinebuffer([]byte(tt.in))
			var lines []string
			for _, line := range v.lines {
				assert.True(t, utf8.Valid(line))
				lines = append(lines, string(line))
			}
			assert.Equal(t, tt.out, lines)
		})
	}
}

func TestViewportPadsByTerminalColumns(t *testing.T) {
	v := &viewport{width: 4, height: 10}
	v.appendToLinebuffer([]byte("界"))
	assert.Equal(t, []string{"界  \n"}, []string{string(v.lines[0])})
}

func TestViewportKeepsRuneSplitAcrossWrites(t *testing.T) {
	notify := make(chan viewportChanged, 10)
	v := initViewport(t.Context(), notify, 4, 10)
	for _, chunk := range []string{"ab\xe7", "\x95", "\x8ccd"} {
		_, err := v.Write([]byte(chunk))
		assert.NoError(t, err)
		<-notify
	}
	var buf bytes.Buffer
	_, err := v.WriteTo(&buf)
	assert.NoError(t, err)
	assert.True(t, utf8.ValidString(buf.String()))
	assert.Equal(t, "\rab  \n\r界cd\n", buf.String())
}

func TestViewportJoinsSplitWrites(t *testing.T) {
	for _, tt := range []struct {
		name   string
		writes []string
		out    []string
	}{
		{"rune", []string{"ab\xe7", "\x95", "\x8ccd"}, []string{"ab\n", "界cd\n"}},
		{"CSI", []string{"ab\x1b[1", ";31", "mcdéfg"}, []string{"ab\n", "\x1b[1;31mcdéf\n", "g\n"}},
		{"lone ESC", []string{"ab\x1b", "[0mcd"}, []string{"ab\n", "\x1b[0mcd\n"}},
		{"OSC 8 link", []string{"\x1b]8;;http://a", "\x1b", "\\link"}, []string{"\x1b]8;;http://a\x1b\\link\n"}},
		{"broken CSI", []string{"ab\x1b[31\ncd"}, []string{"ab\n", "cd\n"}},
		{"broken ESC", []string{"ab\x1b\x1b[0mc"}, []string{"ab\x1b[0mc\n"}},
		{"broken after a split", []string{"ab\x1b]0;t", "\x1b[0mc"}, []string{"ab\n", "\x1b[0mc\n"}},
		{"carriage return joins a cluster", []string{"abc\re\r\u0301x"}, []string{"abce\u0301\n", "x\n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := &viewport{width: 4, height: 10, fixedHeight: true}
			for _, w := range tt.writes {
				v.appendToLinebuffer(v.joinPartial([]byte(w)))
			}
			assert.Equal(t, 0, len(v.partial))
			var lines []string
			for _, line := range v.lines {
				lines = append(lines, string(line))
			}
			assert.Equal(t, tt.out, lines)
		})
	}
}

func TestViewportDropsLongSequence(t *testing.T) {
	v := &viewport{width: 4, height: 10, fixedHeight: true}
	title := "\x1b]0;" + strings.Repeat("x", maxHeldBytes)
	assert.Equal(t, 0, len(v.joinPartial([]byte(title[:4]))))
	assert.Equal(t, 4, len(v.partial))
	// the terminator is too far away to wait for, so the sequence is dropped
	assert.Equal(t, 0, len(v.joinPartial([]byte(title[4:]))))
	assert.Equal(t, 0, len(v.partial))
	// text before the oversized sequence is kept
	assert.Equal(t, "ab", string(v.joinPartial([]byte("ab"+title))))
}
