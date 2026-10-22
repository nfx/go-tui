// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package progress

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/nfx/go-tui/internal/errs"
)

var runtimeNumCPU = runtime.NumCPU

type parallelProgressRunner[T any] struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	p        *Progressbar
	slice    []T
	yield    func(T) error
	jobs     chan int
	wg       sync.WaitGroup
	errOnce  sync.Once
	firstErr error
}

type panicError struct {
	err   error
	stack []byte
}

const (
	panicSkipFrames    = 4
	stackLinesPerFrame = 2
)

func (p *panicError) Error() string {
	stack := strings.TrimSuffix(string(p.stack), "\n")
	if stack == "" {
		return p.err.Error()
	}
	return p.err.Error() + "\n\n" + stack
}

func (p *panicError) Unwrap() error {
	return p.err
}

// run executes all work items and returns the first callback error if any.
func (r *parallelProgressRunner[T]) run() error {
	r.startWorkers()
	r.enqueueJobs()
	close(r.jobs)
	r.wg.Wait()
	if r.firstErr != nil {
		return r.firstErr
	}
	return r.ctx.Err()
}

// startWorkers starts worker goroutines that consume job indexes.
func (r *parallelProgressRunner[T]) startWorkers() {
	for range r.p.effectiveWorkers(len(r.slice)) {
		r.wg.Add(1)
		go r.worker()
	}
}

// worker processes jobs and reports only the first callback error.
func (r *parallelProgressRunner[T]) worker() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case idx, ok := <-r.jobs:
			if !ok {
				return
			}
			err := r.runYield(r.slice[idx])
			if err != nil {
				r.setErr(err)
				continue
			}
			r.p.Add(1)
		}
	}
}

func (r *parallelProgressRunner[T]) runYield(v T) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &panicError{
				err:   fmt.Errorf("%w: panic: %v", errs.ErrBug, recovered),
				stack: r.stackTrace(panicSkipFrames),
			}
		}
	}()
	return r.yield(v)
}

func (r *parallelProgressRunner[T]) stackTrace(skip int) []byte {
	raw := debug.Stack()
	if skip <= 0 {
		return raw
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 {
		return raw
	}
	header := lines[0]
	body := lines[1:]
	toDrop := skip * stackLinesPerFrame
	if toDrop > len(body) {
		toDrop = len(body)
	}
	body = body[toDrop:]
	return []byte(strings.Join(append([]string{header}, body...), "\n"))
}

// setErr records only the first callback error and cancels the remaining work.
func (r *parallelProgressRunner[T]) setErr(err error) {
	if err == nil {
		return
	}
	r.errOnce.Do(func() {
		r.firstErr = err
		r.cancel(err)
	})
}

// enqueueJobs sends item indexes to workers until context cancellation.
func (r *parallelProgressRunner[T]) enqueueJobs() {
	for idx := range len(r.slice) {
		select {
		case <-r.ctx.Done():
			return
		case r.jobs <- idx:
		}
	}
}
