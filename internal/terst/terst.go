// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"time"

	xterm "golang.org/x/term"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"
	"sync/atomic"
)

// SyncBuffer is a bytes.Buffer safe for one writer goroutine and concurrent readers.
type SyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *SyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *SyncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *SyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type FDByteReader struct {
	F *os.File
}

// Read returns single bytes so ReadRune sees one key at a time.
func (r *FDByteReader) Read(p []byte) (int, error) {
	buf := make([]byte, 1)
	n, err := r.F.Read(buf)
	if n > 0 {
		p[0] = buf[0]
		return 1, nil
	}
	return n, err
}

func (r *FDByteReader) Fd() uintptr {
	return r.F.Fd()
}

func StartChanIO(ctx context.Context, width, height int) *term.ChanIO {
	return startChanIOTo(ctx, io.Discard, width, height)
}

func NewTermIO(width, height int) *term.IO {
	return &term.IO{
		In:      bytes.NewBuffer(nil),
		Out:     &bytes.Buffer{},
		Width:   width,
		Height:  height,
		Restore: func() error { return nil },
	}
}

type ChunkReader struct {
	Chunks [][]byte
	idx    int
}

// Read returns the next chunk to simulate multi-byte key sequences.
func (r *ChunkReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.Chunks) {
		return 0, io.EOF
	}
	chunk := r.Chunks[r.idx]
	r.idx++
	n := copy(p, chunk)
	return n, nil
}

// FailReader fails the test when a prompt reads from it.
type FailReader struct{ t *testing.T }

func (r FailReader) Read([]byte) (int, error) {
	r.t.Helper()
	r.t.Error("automatic answer must not read input")
	return 0, io.EOF
}

// Quiet makes the default IO a prompt without terminal for the test: no input
// may be read, and the output must stay empty.
func Quiet(t *testing.T) {
	t.Helper()
	StrictOutput(t)
	out := &bytes.Buffer{}
	t.Cleanup(func() { assert.Equal(t, "", out.String()) })
	UseIO(t, FailReader{t}, out)
}

// KeepTheme restores the installed theme after a test.
func KeepTheme(t *testing.T) *render.Compiled {
	t.Helper()
	prev := term.Default.CurrentTheme()
	t.Cleanup(func() { term.Default.Theme.Store(prev) })
	return prev
}

// WithEnv installs an output environment fixture built from KEY=VALUE pairs.
func WithEnv(t *testing.T, pairs ...string) {
	t.Helper()
	vars := map[string]string{}
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		vars[k] = v
	}
	prev := term.Default.Env
	term.Default.Env = term.ReadOutputEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
	t.Cleanup(func() { term.Default.Env = prev })
}

func TTYWriter(t *testing.T) *MockDescriptor {
	t.Helper()
	prev := term.WriterChecker
	term.WriterChecker = func(int) bool { return true }
	prevVT := term.ConsoleVT
	term.ConsoleVT = func(io.Writer) bool { return true }
	t.Cleanup(func() {
		term.WriterChecker = prev
		term.ConsoleVT = prevVT
	})
	return &MockDescriptor{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}, FD: 7}
}

func WaitOutputContains(t *testing.T, out <-chan string, parts ...string) string {
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

// Recv returns the next value from ch, failing the test after a second.
func Recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(time.Second):
		var zero T
		t.Fatalf("timed out receiving %T", zero)
		return zero
	}
}

// Send delivers v on ch, failing the test after a second.
func Send[T any](t *testing.T, ch chan<- T, v T) {
	t.Helper()
	select {
	case ch <- v:
	case <-time.After(time.Second):
		t.Fatalf("timed out sending %T", v)
	}
}

func ChainIOForTest(t *testing.T, width, height int) (*term.ChanIO, *term.WriteC) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	realOut := term.NewWriteC(ctx)
	cio := term.NewUnstartedIO(ctx, width, height, 0)
	go cio.HandleViewports(ctx)
	go cio.ForwardTo(ctx, realOut)
	t.Cleanup(func() {
		cancel()
		close(cio.In)
		close(cio.Out)
	})
	return cio, realOut
}

type FailingWriter struct{ Err error }

func (w FailingWriter) Write([]byte) (int, error) { return 0, w.Err }

type MockDescriptor struct {
	io.Reader
	io.Writer
	FD uintptr
}

func (m *MockDescriptor) Fd() uintptr {
	return m.FD
}

// OSCReply is what a terminal sends for an OSC 11 query.
const OSCReply = "\x1b]11;rgb:1e1e/1e1e/2e2e\x07"

// ReplyingTerminal answers OSC 11 queries through the input pipe.
type ReplyingTerminal struct {
	mu      sync.Mutex
	queries int
	reply   string
	in      *io.PipeWriter
}

// Write counts queries and injects the configured reply.
func (r *ReplyingTerminal) Write(p []byte) (int, error) {
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

// Count returns the synchronized query Count.
func (r *ReplyingTerminal) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queries
}

// ProbeTermIO creates a simulated terminal with automatic cleanup.
func ProbeTermIO(t *testing.T, reply string) (*term.IO, *ReplyingTerminal) {
	t.Helper()
	prev, prevSupported := term.InputChecker, term.ProbeSupported
	term.InputChecker = func(int) bool { return true }
	term.ProbeSupported = true // the simulated terminal accepts VT input on every platform
	t.Cleanup(func() { term.InputChecker, term.ProbeSupported = prev, prevSupported })
	pr, pw := io.Pipe()
	t.Cleanup(func() { assert.NoError(t, pw.Close()) })
	rt := &ReplyingTerminal{reply: reply, in: pw}
	return &term.IO{In: &MockDescriptor{Reader: pr, FD: 7}, Out: rt, Fd: 2}, rt
}

// StrictOutput restores production terminal classification for a test, where
// writers without terminal capabilities are not terminals.
func StrictOutput(t *testing.T) {
	t.Helper()
	prev := term.OutputIsTerminal
	term.OutputIsTerminal = term.HostsTerminal
	t.Cleanup(func() { term.OutputIsTerminal = prev })
}

// ProbingKeyboard makes input on fd a terminal that answers background
// queries, without changing any terminal mode.
func ProbingKeyboard(t *testing.T, fd uintptr) {
	t.Helper()
	prevInput, prevSupported := term.InputChecker, term.ProbeSupported
	prevRaw, prevRestore := term.MakeRaw, term.Restore
	term.InputChecker = func(f int) bool { return f == int(fd) || prevInput(f) }
	term.ProbeSupported = true
	term.MakeRaw = func(int) (*xterm.State, error) { return &xterm.State{}, nil }
	term.Restore = func(int, *xterm.State) error { return nil }
	t.Cleanup(func() {
		term.InputChecker, term.ProbeSupported = prevInput, prevSupported
		term.MakeRaw, term.Restore = prevRaw, prevRestore
	})
}

// ASCIISymbols replace the presets on terminals without the Unicode glyphs.
var ASCIISymbols = theme.New().Symbols.ASCII()

// BlockingByteReader feeds scripted chunks to a blocking Read.
type BlockingByteReader struct {
	ch      chan []byte
	waiters atomic.Int32
}

// NewBlockingByteReader returns a reader whose Read blocks until a chunk is
// sent; buffer is the number of chunks that can be sent without a reader.
func NewBlockingByteReader(buffer int) *BlockingByteReader {
	return &BlockingByteReader{ch: make(chan []byte, buffer)}
}

func (r *BlockingByteReader) Read(p []byte) (int, error) {
	r.waiters.Add(1)
	chunk, ok := <-r.ch
	r.waiters.Add(-1)
	if !ok {
		return 0, io.EOF
	}
	if len(chunk) == 0 || len(p) == 0 {
		return 0, nil
	}
	n := copy(p, chunk)
	return n, nil
}

func (r *BlockingByteReader) SendByte(b byte) {
	r.SendBytes([]byte{b})
}

func (r *BlockingByteReader) SendBytes(bs []byte) {
	cp := make([]byte, len(bs))
	copy(cp, bs)
	r.ch <- cp
}

func (r *BlockingByteReader) Waiters() int {
	return int(r.waiters.Load())
}

func (r *BlockingByteReader) Close() {
	close(r.ch)
}

// FrameWriter delivers every write as one receive. Unlike a [term.ChanIO]
// without a viewport, spinners clear their rows on it when they stop.
type FrameWriter chan string

func (w FrameWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

// StripSGR removes SGR sequences, keeping text and every other sequence.
func StripSGR(s string) string {
	var out ansi.Text
	for seg := range ansi.Text(s).Segments() {
		if _, sgr := seg.SGRParams(); seg.IsEscape && sgr {
			continue
		}
		out = append(out, seg.Text...)
	}
	return out.String()
}

// ASCIITheme is the preset theme with ASCII symbols, Guided or Inline.
func ASCIITheme(inline bool) theme.Theme {
	th := theme.New()
	th.Symbols = ASCIISymbols
	th.Inline = inline
	return th
}

// NotifyingReader exposes a descriptor while reporting when a blocking read starts.
type NotifyingReader struct {
	*os.File
	Started chan struct{}
	once    sync.Once
}

// NewNotifyingReader wraps f.
func NewNotifyingReader(f *os.File) *NotifyingReader {
	return &NotifyingReader{File: f, Started: make(chan struct{})}
}

// Read signals entry before reading one byte, like a wrapper that limits chunks.
func (r *NotifyingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.Started) })
	return r.File.Read(p[:1])
}

// NextKey reads the next key of in through a fresh terminal, the way the next
// prompt on the same input would after a cancelled one.
func NextKey(t *testing.T, in io.Reader) rune {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tio := &term.IO{In: in, Input: term.SharedInputState(in), Width: 20, Height: 2}
	permits := make(chan struct{}, 1)
	permits <- struct{}{}
	keys := tio.ReadEvents(ctx, permits)
	defer tio.AwaitReader()
	defer cancel()
	ev := <-keys
	assert.NoError(t, ev.Err)
	return ev.Key
}

// UseIO makes in and out the default input and output for the test, like the
// stdin and stderr of a CLI. Cleanup closes the coordinated writer and
// restores os.Stdin and os.Stderr.
func UseIO(t *testing.T, in io.Reader, out io.Writer) {
	t.Helper()
	term.Default.SetIO(in, out)
	t.Cleanup(func() {
		// close the arbiter only: the test owns out
		assert.NoError(t, term.Default.Close())
		term.Default.SetIO(os.Stdin, os.Stderr)
	})
}

// UseTheme installs th and routes migrated widgets through it for the test.
func UseTheme(t *testing.T, th theme.Theme) {
	t.Helper()
	KeepTheme(t)
	assert.NoError(t, term.Default.InstallTheme(th))
}

func startChanIOTo(ctx context.Context, out io.Writer, width, height int) *term.ChanIO {
	cio := term.NewUnstartedIO(ctx, width, height, 0)
	go cio.HandleViewports(ctx)
	go cio.ForwardTo(ctx, out)
	return cio
}

// Main patches the terminal seams for a package test binary, then runs the
// tests. Most tests drive interactive widgets through in-memory writers, so
// the binary treats them as a terminal. Tests of the production routing call
// [StrictOutput]. Environment fixtures read the same on every platform, and
// the suite drives terminals the same in a CI job.
func Main(m *testing.M) {
	term.LegacyConsoles = false
	term.Default.Env.Ci = false
	term.OutputIsTerminal = func(w io.Writer) bool {
		if term.HostsTerminal(w) {
			return true
		}
		_, isFD := w.(term.Descriptor)
		return !isFD
	}
	os.Exit(m.Run())
}
