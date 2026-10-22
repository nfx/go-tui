// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/render"
	"io"
	"iter"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"
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
func quiet(t *testing.T) option.Opt {
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

// withEnv installs an output environment fixture built from KEY=VALUE pairs.
func withEnv(t *testing.T, pairs ...string) {
	t.Helper()
	vars := map[string]string{}
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		vars[k] = v
	}
	prev := defaultIO.env
	defaultIO.env = readOutputEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
	t.Cleanup(func() { defaultIO.env = prev })
}

func ttyWriter(t *testing.T) *mockDescriptor {
	t.Helper()
	prev := terminalWriterChecker
	terminalWriterChecker = func(int) bool { return true }
	prevVT := consoleVT
	consoleVT = func(io.Writer) bool { return true }
	t.Cleanup(func() {
		terminalWriterChecker = prev
		consoleVT = prevVT
	})
	return &mockDescriptor{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}, fd: 7}
}

func waitOutputContains(t *testing.T, out <-chan string, parts ...string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-out:
			matched := true
			for _, part := range parts {
				if !strings.Contains(got, part) {
					matched = false
					break
				}
			}
			if matched {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for output containing %q", parts)
		}
	}
}

var regexpSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func chainIOforTest(t *testing.T, width, height int) (*chanIO, *writeC) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	realOut := newWriteC(ctx)
	cio := newUnstartedIO(ctx, width, height, 0)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, realOut)
	t.Cleanup(func() {
		cancel()
		close(cio.In)
		close(cio.Out)
	})
	return cio, realOut
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestChanIOFlushAndCloseReportWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cio := newUnstartedIO(ctx, 80, 24, 0)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, failingWriter{io.ErrShortWrite})
	tio := &tio{arbiter: cio, cancel: cancel}

	_, err := cio.Write([]byte("lost\n"))
	assert.NoError(t, err) // queueing is asynchronous
	assert.ErrorIs(t, cio.Flush(), io.ErrShortWrite)
	assert.ErrorIs(t, tio.Flush(), io.ErrShortWrite)
	assert.ErrorIs(t, tio.Close(), io.ErrShortWrite)
}

type mockDescriptor struct {
	io.Reader
	io.Writer
	fd uintptr
}

func (m *mockDescriptor) Fd() uintptr {
	return m.fd
}

// oscReply is what a terminal sends for an OSC 11 query.
const oscReply = "\x1b]11;rgb:1e1e/1e1e/2e2e\x07"

// replyingTerminal answers OSC 11 queries through the input pipe.
type replyingTerminal struct {
	mu      sync.Mutex
	queries int
	reply   string
	in      *io.PipeWriter
}

// Write counts queries and injects the configured reply.
func (r *replyingTerminal) Write(p []byte) (int, error) {
	if string(p) != "\x1b]11;?\x07" {
		return len(p), nil
	}
	r.mu.Lock()
	r.queries++
	r.mu.Unlock()
	if r.reply != "" {
		go r.in.Write([]byte(r.reply)) //nolint:errcheck
	}
	return len(p), nil
}

// count returns the synchronized query count.
func (r *replyingTerminal) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queries
}

// probeTermIO creates a simulated terminal with automatic cleanup.
func probeTermIO(t *testing.T, reply string) (*termIO, *replyingTerminal) {
	t.Helper()
	prev, prevSupported := terminalInputChecker, backgroundProbeSupported
	terminalInputChecker = func(int) bool { return true }
	backgroundProbeSupported = true // the simulated terminal accepts VT input on every platform
	t.Cleanup(func() { terminalInputChecker, backgroundProbeSupported = prev, prevSupported })
	pr, pw := io.Pipe()
	t.Cleanup(func() { assert.NoError(t, pw.Close()) })
	term := &replyingTerminal{reply: reply, in: pw}
	return &termIO{in: &mockDescriptor{Reader: pr, fd: 7}, out: term, fd: 2}, term
}
