// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"bytes"
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

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

func startChanIO(ctx context.Context) *ChanIO {
	return startChanIOTo(ctx, io.Discard, 80, 24)
}

func startChanIOTo(ctx context.Context, out io.Writer, width, height int) *ChanIO {
	cio := NewUnstartedIO(ctx, width, height, 0)
	go cio.HandleViewports(ctx)
	go cio.ForwardTo(ctx, out)
	return cio
}

// withEnv installs an output environment fixture built from KEY=VALUE pairs.
func withEnv(t *testing.T, pairs ...string) {
	t.Helper()
	vars := map[string]string{}
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		vars[k] = v
	}
	prev := Default.Env
	Default.Env = ReadOutputEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
	t.Cleanup(func() { Default.Env = prev })
}

func ttyWriter(t *testing.T) *mockDescriptor {
	t.Helper()
	prev := WriterChecker
	WriterChecker = func(int) bool { return true }
	prevVT := ConsoleVT
	ConsoleVT = func(io.Writer) bool { return true }
	t.Cleanup(func() {
		WriterChecker = prev
		ConsoleVT = prevVT
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

func chainIOforTest(t *testing.T, height int) (*ChanIO, *WriteC) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	realOut := NewWriteC(ctx)
	cio := NewUnstartedIO(ctx, 40, height, 0)
	go cio.HandleViewports(ctx)
	go cio.ForwardTo(ctx, realOut)
	t.Cleanup(func() {
		cancel()
		close(cio.In)
		close(cio.Out)
	})
	return cio, realOut
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
func probeTermIO(t *testing.T, reply string) (*IO, *replyingTerminal) {
	t.Helper()
	prev, prevSupported := InputChecker, ProbeSupported
	InputChecker = func(int) bool { return true }
	ProbeSupported = true // the simulated terminal accepts VT input on every platform
	t.Cleanup(func() { InputChecker, ProbeSupported = prev, prevSupported })
	pr, pw := io.Pipe()
	t.Cleanup(func() { assert.NoError(t, pw.Close()) })
	rt := &replyingTerminal{reply: reply, in: pw}
	return &IO{In: &mockDescriptor{Reader: pr, fd: 7}, Out: rt, Fd: 2}, rt
}

// strictOutput restores production terminal classification for a test, where
// writers without terminal capabilities are not terminals.
func strictOutput(t *testing.T) {
	t.Helper()
	prev := OutputIsTerminal
	OutputIsTerminal = HostsTerminal
	t.Cleanup(func() { OutputIsTerminal = prev })
}

// Most tests drive interactive widgets through in-memory writers, so the test
// binary treats them as a terminal. Tests of the production routing call
// [strictOutput]. Environment fixtures read the same on every platform, and
// the suite drives terminals the same in a CI job.
func TestMain(m *testing.M) {
	LegacyConsoles = false
	Default.Env.Ci = false
	OutputIsTerminal = func(w io.Writer) bool {
		if HostsTerminal(w) {
			return true
		}
		_, isFD := w.(Descriptor)
		return !isFD
	}
	os.Exit(m.Run())
}
