// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package progress

import (
	"errors"
	"fmt"
	"io"
	"os"
)

type fileStat interface {
	Stat() (os.FileInfo, error)
}

type sized interface {
	Size() int64
}

var errNoSize = errors.New("unable to determine size of reader")

type WrapReader struct {
	r io.Reader
	p *Progressbar
}

func (w *WrapReader) Size() (int64, error) {
	f, ok := w.r.(fileStat)
	if ok {
		fi, err := f.Stat()
		if err != nil {
			return 0, err
		}
		return fi.Size(), nil
	}
	s, ok := w.r.(sized)
	if ok {
		return s.Size(), nil
	}
	return 0, errNoSize
}

func (w *WrapReader) Read(p []byte) (n int, err error) {
	n, err = w.r.Read(p)
	if n > 0 {
		w.p.Add(int64(n))
	}
	if errors.Is(err, io.EOF) {
		if w.p.io == nil && w.p.ctx.Err() == nil {
			w.p.logProgress(0, true)
		}
		w.p.cancel(nil)
	}
	return n, err
}

func (w *WrapReader) Close() error {
	var errs []error
	if err := w.p.Close(); err != nil {
		errs = append(errs, fmt.Errorf("progress: %w", err))
	}
	if closer, ok := w.r.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
