// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/render"
	"io"
	"iter"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"text/template"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/assert"
)

// syncBuffer is a bytes.Buffer safe for one writer goroutine and concurrent readers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fdByteReader struct {
	f *os.File
}

// Read returns single bytes so ReadRune sees one key at a time.
func (r *fdByteReader) Read(p []byte) (int, error) {
	buf := make([]byte, 1)
	n, err := r.f.Read(buf)
	if n > 0 {
		p[0] = buf[0]
		return 1, nil
	}
	return n, err
}

func (r *fdByteReader) Fd() uintptr {
	return r.f.Fd()
}

func startChanIO(ctx context.Context, width, height int) *chanIO {
	return startChanIOTo(ctx, io.Discard, width, height)
}

func startChanIOTo(ctx context.Context, out io.Writer, width, height int) *chanIO {
	cio := newUnstartedIO(ctx, width, height, 0)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, out)
	return cio
}

func newTestTermIO(width, height int) *termIO {
	return &termIO{
		in:      bytes.NewBuffer(nil),
		out:     &bytes.Buffer{},
		Width:   width,
		Height:  height,
		Restore: func() error { return nil },
	}
}

type chunkReader struct {
	chunks [][]byte
	idx    int
}

// Read returns the next chunk to simulate multi-byte key sequences.
func (r *chunkReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.idx]
	r.idx++
	n := copy(p, chunk)
	return n, nil
}

// failReader fails the test when a prompt reads from it.
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Helper()
	r.t.Error("automatic answer must not read input")
	return 0, io.EOF
}

// quiet configures a prompt without terminal: no input may be read, and the
// buffer must stay empty.
func quiet(t *testing.T) Opt {
	t.Helper()
	strictOutput(t)
	out := &bytes.Buffer{}
	t.Cleanup(func() { assert.Equal(t, "", out.String()) })
	return WithOptions(WithInput(failReader{t}), WithOutput(out))
}

type server struct {
	Name string
	Port int
}

type tagged struct {
	Name string
	ID   int
}

// seq yields values and records whether it ran.
func seq[T any](started *bool, err error, values ...T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		*started = true
		for _, v := range values {
			if !yield(v, nil) {
				return
			}
		}
		if err != nil {
			var zero T
			yield(zero, err)
		}
	}
}

// boundedScreen is a terminal of a fixed size that scrolls: a newline on the
// bottom row moves the top row into scrolled. It knows what newline-terminated
// frames and their cleanup write: CR, LF, cursor up and down, and erase line.
type boundedScreen struct {
	cells    [][]rune
	scrolled []string
	row, col int
}

func newBoundedScreen(width, height int) *boundedScreen {
	s := &boundedScreen{}
	for range height {
		s.cells = append(s.cells, []rune(strings.Repeat(" ", width)))
	}
	return s
}

// write applies out to the screen.
func (s *boundedScreen) write(out string) {
	for i := 0; i < len(out); {
		r, size := utf8.DecodeRuneInString(out[i:])
		i += size
		switch {
		case r == '\r':
			s.col = 0
		case r == '\n' && s.row == len(s.cells)-1:
			s.scrolled = append(s.scrolled, strings.TrimSpace(string(s.cells[0])))
			s.cells = append(s.cells[1:], []rune(strings.Repeat(" ", len(s.cells[0]))))
		case r == '\n':
			s.row++
		case r == 0x1b && strings.HasPrefix(out[i:], "["):
			end := strings.IndexFunc(out[i+1:], func(r rune) bool { return r >= 0x40 && r <= 0x7e }) + i + 1
			n, err := strconv.Atoi(out[i+1 : end])
			if err != nil {
				n = 1
			}
			switch out[end] {
			case 'A':
				s.row = max(0, s.row-n)
			case 'B':
				s.row = min(len(s.cells)-1, s.row+n)
			case 'K':
				for c := s.col; c < len(s.cells[s.row]); c++ {
					s.cells[s.row][c] = ' '
				}
			}
			i = end + 1
		case s.col < len(s.cells[s.row]):
			s.cells[s.row][s.col] = r
			s.col++
		}
	}
}

// visible returns the rows that are not blank, without surrounding spaces.
func (s *boundedScreen) visible() (rows []string) {
	for _, line := range s.cells {
		if row := strings.TrimSpace(string(line)); row != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

// keepTheme restores the installed theme after a test.
func keepTheme(t *testing.T) *render.Compiled {
	t.Helper()
	prev := defaultIO.currentTheme()
	t.Cleanup(func() { defaultIO.theme.Store(prev) })
	return prev
}

// installedFuncs are the helpers of the installed theme, for a plain destination.
func installedFuncs() template.FuncMap {
	return defaultIO.currentTheme().Funcs(color.ProfilePlain, render.Roles{})
}

// execute renders src with funcs and fails the test on any error.
func execute(t *testing.T, funcs template.FuncMap, src string) string {
	t.Helper()
	tmpl, err := template.New("t").Funcs(funcs).Parse(src)
	assert.NoError(t, err)
	var b strings.Builder
	assert.NoError(t, tmpl.Execute(&b, nil))
	return b.String()
}
