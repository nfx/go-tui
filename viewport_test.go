// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

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
