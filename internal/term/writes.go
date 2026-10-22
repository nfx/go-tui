// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"context"
	"io"
)

// failWriter records the first error of the underlying terminal writer.
type failWriter struct {
	w   io.Writer
	err error
}

// Write retains the first output failure, treating incomplete writes as errors.
func (f *failWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if err != nil && f.err == nil {
		f.err = err
	}
	return n, err
}

type externalNewlineState struct {
	prevCR bool
}

// normalize rewrites lone LF bytes to CRLF for raw terminal output.
func (s *externalNewlineState) normalize(p []byte) []byte {
	if len(p) == 0 {
		return nil
	}
	out := make([]byte, 0, len(p))
	for _, b := range p {
		if b == '\n' && !s.prevCR {
			out = append(out, '\r')
		}
		out = append(out, b)
		s.prevCR = b == '\r'
	}
	return out
}

func (s *externalNewlineState) normalizeString(x string) []byte {
	return s.normalize([]byte(x))
}

// controlWrite is a request for the arbiter to write bytes verbatim.
type controlWrite struct {
	p     []byte
	reply chan error
}

func NewWriteC(ctx context.Context) *WriteC {
	return &WriteC{
		Context: ctx,
		C:       make(chan string, 128),
	}
}

type WriteC struct {
	context.Context
	C chan string
}

func (x *WriteC) Write(p []byte) (n int, err error) {
	select {
	case <-x.Done():
		return 0, io.EOF
	case x.C <- string(p):
		return len(p), nil
	}
}
