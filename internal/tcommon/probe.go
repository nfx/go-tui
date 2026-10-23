// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tcommon

import (
	"io"
	"sync"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/term"
)

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
	Swap(t, &term.InputChecker, func(int) bool { return true })
	Swap(t, &term.ProbeSupported, true) // the simulated terminal accepts VT input on every platform
	pr, pw := io.Pipe()
	t.Cleanup(func() { assert.NoError(t, pw.Close()) })
	rt := &ReplyingTerminal{reply: reply, in: pw}
	return &term.IO{In: &MockDescriptor{Reader: pr, FD: 7}, Out: rt, Fd: 2}, rt
}
