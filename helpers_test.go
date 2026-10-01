// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"io"
	"os"
)

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
	cio := newUnstartedIO(ctx, width, height, 0)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, io.Discard)
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
