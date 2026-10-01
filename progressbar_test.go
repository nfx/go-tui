// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestProgressStateRenderFitsWidth(t *testing.T) {
	state := &progressState{
		maxNum:       100,
		currentNum:   50,
		showRate:     true,
		fmtRate:      func(float64) string { return "10.00" },
		rollingRates: []float64{10},
	}

	now := time.Date(2024, time.January, 1, 0, 0, 5, 0, time.UTC)

	for _, availWidth := range []int{30, 20, 12, 8, 4, 1} {
		var frame bytes.Buffer
		err := state.render(&frame, availWidth, now)
		assert.NoError(t, err)
		assert.True(t, width(frame.Bytes()) <= availWidth)
	}
}

func TestProgressbarLinesToClearShrinks(t *testing.T) {
	p := &Progressbar{
		lastFrameWidth: 80,
	}
	assert.Equal(t, 2, p.linesToClear(40))
	assert.Equal(t, 1, p.linesToClear(0))
}

func TestProgressbarTickLeavesAutowrapColumnFree(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := start

	p, err := newStartedProgressBar("download", 20,
		WithInput(cio),
		WithOutput(cio),
		progressbarOpt(func(pb *Progressbar) error {
			pb.now = func() time.Time { return now }
			pb.redrawAt = start
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   40,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)
	assert.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, p.Close())
	})

	p.Add(10)
	now = start.Add(time.Second)

	select {
	case ticks <- now:
	case <-time.After(time.Second):
		t.Fatalf("tick not delivered")
	}

	var output string
	select {
	case output = <-cio.Out:
	case <-time.After(time.Second):
		t.Fatalf("no progress output")
	}

	assert.Equal(t, 39, width([]byte(output)))
}

func mustReceiveProgressEvent(t *testing.T, ch <-chan progressEvent) progressEvent {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for progress event")
		return nil
	}
}

func TestProgressbarTickRenders(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := start

	p, err := newStartedProgressBar("download", 20,
		WithInput(cio),
		WithOutput(cio),
		progressbarOpt(func(pb *Progressbar) error {
			pb.now = func() time.Time { return now }
			pb.redrawAt = start
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   40,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)
	assert.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, p.Close())
	})

	p.Add(10)
	now = start.Add(time.Second)

	select {
	case ticks <- now:
	case <-time.After(time.Second):
		t.Fatalf("tick not delivered")
	}

	var output string
	select {
	case output = <-cio.Out:
	case <-time.After(time.Second):
		t.Fatalf("no progress output")
	}

	assert.NotContains(t, output, "\x1b[1A\r\x1b[K\r")
	assert.Contains(t, output, "download 50% [>](10.00/s, 1s remaining)")

	p.Add(5)
	now = start.Add(2 * time.Second)

	select {
	case ticks <- now:
	case <-time.After(time.Second):
		t.Fatalf("tick not delivered")
	}

	var second string
	select {
	case second = <-cio.Out:
	case <-time.After(time.Second):
		t.Fatalf("no second progress output")
	}

	assert.Contains(t, second, "\x1b[1A\r\x1b[K\r")
	assert.Contains(t, second, "download 75%")
}

func TestProgressbarEmitsStructuredEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := start
	events := make(chan progressEvent, 16)

	p, err := newStartedProgressBar("download", 20,
		WithInput(cio),
		WithOutput(cio),
		progressbarOpt(func(pb *Progressbar) error {
			pb.eventSink = func(ev progressEvent) {
				events <- ev
			}
			pb.now = func() time.Time { return now }
			pb.redrawAt = start
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   40,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)
	assert.NoError(t, err)

	initEv, ok := mustReceiveProgressEvent(t, events).(progressInit)
	assert.True(t, ok)
	assert.Equal(t, "download", initEv.Label)
	assert.Equal(t, int64(20), initEv.Max)

	p.Add(10)

	now = start.Add(time.Second)
	select {
	case ticks <- now:
	case <-time.After(time.Second):
		t.Fatalf("tick not delivered")
	}
	select {
	case <-cio.Out:
	case <-time.After(time.Second):
		t.Fatalf("no progress output")
	}
	metrics, ok := mustReceiveProgressEvent(t, events).(progressUpdate)
	assert.True(t, ok)
	assert.Equal(t, int64(time.Second), metrics.Remaining)
	assert.Equal(t, int64(0), metrics.Elapsed)

	assert.NoError(t, p.Close())
	closed, ok := mustReceiveProgressEvent(t, events).(progressClosed)
	assert.True(t, ok)
	assert.Equal(t, "download", closed.Label)
}

func TestProgressbarDoneEmitsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := start
	events := make(chan progressEvent, 16)

	p, err := newStartedProgressBar("sync", 2,
		WithInput(cio),
		WithOutput(cio),
		progressbarOpt(func(pb *Progressbar) error {
			pb.eventSink = func(ev progressEvent) {
				events <- ev
			}
			pb.now = func() time.Time { return now }
			pb.redrawAt = start
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   40,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)
	assert.NoError(t, err)

	_, ok := mustReceiveProgressEvent(t, events).(progressInit)
	assert.True(t, ok)

	p.Add(2)

	now = start.Add(time.Second)
	select {
	case ticks <- now:
	case <-time.After(time.Second):
		t.Fatalf("tick not delivered")
	}
	select {
	case <-cio.Out:
	case <-time.After(time.Second):
		t.Fatalf("no progress output")
	}
	_, ok = mustReceiveProgressEvent(t, events).(progressUpdate)
	assert.True(t, ok)
	closed, ok := mustReceiveProgressEvent(t, events).(progressClosed)
	assert.True(t, ok)
	assert.Equal(t, "sync", closed.Label)
}

func TestNewMaxProgressBar(t *testing.T) {
	p, err := NewMaxProgressBar("max", 3)
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, p.Close()) })
	assert.NotNil(t, p)
	assert.Equal(t, int64(3), p.maxNum)

	// Add should be a no-op without a TTY.
	p.Add(3)
}

func TestNewMaxProgressBarAppliesOptions(t *testing.T) {
	_, err := NewMaxProgressBar("max", 1, progressbarOpt(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, errors.New("boom")
		}
		return nil
	}))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

type fakeInfo struct {
	size int64
}

func (f fakeInfo) Name() string       { return "fake" }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() os.FileMode  { return 0 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

type statReader struct {
	data []byte
	pos  int
}

func (r *statReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *statReader) Stat() (os.FileInfo, error) {
	return fakeInfo{size: int64(len(r.data))}, nil
}

func TestNewFileProgressReader(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time, 1)
	applied := false

	r, err := NewFileProgressReader(&statReader{data: []byte("hello world")}, "file",
		WithInput(cio),
		WithOutput(cio),
		progressbarOpt(func(pb *Progressbar) error {
			applied = true
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.increments = make(chan int64, 8)
			pb.now = func() time.Time { return time.Now() }
			pb.redrawAt = pb.now()
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   30,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)
	assert.NoError(t, err)
	assert.True(t, applied)
	assert.NotNil(t, r.p.io)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	_, err = io.ReadAll(r)
	assert.NoError(t, err)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for {
			select {
			case n := <-r.p.increments:
				r.p.currentNum += n
			default:
				goto drained
			}
		}
	drained:
		if r.p.currentNum == 11 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, int64(11), r.p.currentNum)
	assert.Equal(t, int64(11), r.p.maxNum)
}

type sizedReader struct {
	size int64
}

func (r *sizedReader) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (r *sizedReader) Size() int64 {
	return r.size
}

func TestWrapReaderSizeFallback(t *testing.T) {
	r := &wrapReader{r: &sizedReader{size: 42}}

	size, err := r.Size()
	assert.NoError(t, err)
	assert.Equal(t, int64(42), size)
}

func TestWrapReaderSizeUnknown(t *testing.T) {
	_, err := NewFileProgressReader(bytes.NewBufferString("no size"), "file")
	assert.ErrorIs(t, err, errNoSize)
}

func TestNewSliceProgressBar(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 2),
	}
	ticks := make(chan time.Time, 1)
	var seen *Progressbar

	items := []int{1, 2, 3}
	seq := NewSliceProgressBar("items", items,
		WithInput(cio),
		WithOutput(cio),
		progressbarOpt(func(pb *Progressbar) error {
			seen = pb
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.increments = make(chan int64, 8)
			pb.now = func() time.Time { return time.Now() }
			pb.redrawAt = pb.now()
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   30,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)

	var got []int
	for v, err := range seq {
		assert.NoError(t, err)
		got = append(got, v)
	}

	for {
		select {
		case n := <-seen.increments:
			seen.currentNum += n
		default:
			goto drainedSlice
		}
	}
drainedSlice:
	assert.Equal(t, items, got)
	assert.NotNil(t, seen)
	assert.Equal(t, int64(len(items)), seen.currentNum)
	assert.True(t, seen.isDone())
}

func TestWithFormatRate(t *testing.T) {
	p := newProgressbar()
	err := WithFormatRate(func(_ float64) string { return "rate" })(p)
	assert.NoError(t, err)
	if p.fmtRate == nil {
		t.Fatalf("expected format rate function")
	}
}

func TestWithWorkersRejectsNonPositive(t *testing.T) {
	p := newProgressbar()
	err := WithWorkers(0)(p)
	assert.ErrorIs(t, err, ErrInvalidState)
}

func TestWithWorkersSetsConfiguredValue(t *testing.T) {
	p := newProgressbar()
	err := WithWorkers(7)(p)
	assert.NoError(t, err)
	assert.Equal(t, 7, p.workers)
}

func TestNewParallelProgressBarRunsConcurrently(t *testing.T) {
	var active int64
	var maxActive int64
	items := []int{1, 2, 3, 4, 5, 6}
	ready := make(chan struct{})
	var once sync.Once
	err := NewParallelProgressBar("parallel", items, func(v int) error {
		curr := atomic.AddInt64(&active, 1)
		for {
			prev := atomic.LoadInt64(&maxActive)
			if curr <= prev || atomic.CompareAndSwapInt64(&maxActive, prev, curr) {
				break
			}
		}
		if curr >= 3 {
			once.Do(func() {
				close(ready)
			})
		}
		select {
		case <-ready:
		case <-time.After(time.Second):
			return errors.New("timeout waiting for parallel start")
		}
		atomic.AddInt64(&active, -1)
		return nil
	}, WithWorkers(3), progressbarOpt(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	assert.NoError(t, err)
	assert.True(t, maxActive > 1)
}

func TestNewParallelProgressBarUsesDefaultNumCPU(t *testing.T) {
	orig := runtimeNumCPU
	runtimeNumCPU = func() int { return 4 }
	t.Cleanup(func() {
		runtimeNumCPU = orig
	})
	p := newProgressbar()
	assert.Equal(t, 4, p.workers)
	items := []int{1, 2, 3, 4}
	var active int64
	var maxActive int64
	ready := make(chan struct{})
	var once sync.Once
	err := NewParallelProgressBar("parallel", items, func(v int) error {
		curr := atomic.AddInt64(&active, 1)
		for {
			prev := atomic.LoadInt64(&maxActive)
			if curr <= prev || atomic.CompareAndSwapInt64(&maxActive, prev, curr) {
				break
			}
		}
		if curr >= 4 {
			once.Do(func() {
				close(ready)
			})
		}
		select {
		case <-ready:
		case <-time.After(time.Second):
			return errors.New("timeout waiting for parallel start")
		}
		atomic.AddInt64(&active, -1)
		return nil
	}, progressbarOpt(func(pb *Progressbar) error {
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	assert.NoError(t, err)
	assert.Equal(t, int64(4), maxActive)
}

func TestNewParallelProgressBarFailFastOnFirstError(t *testing.T) {
	items := make([]int, 64)
	for i := range items {
		items[i] = i
	}
	fail := errors.New("fail")
	var processed int64
	err := NewParallelProgressBar("parallel", items, func(v int) error {
		atomic.AddInt64(&processed, 1)
		if v == 7 {
			return fail
		}
		time.Sleep(10 * time.Millisecond)
		return nil
	}, WithWorkers(4), progressbarOpt(func(pb *Progressbar) error {
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	assert.ErrorIs(t, err, fail)
	assert.True(t, processed < int64(len(items)))
}

func TestNewParallelProgressBarRecoversPanicsAsBug(t *testing.T) {
	err := NewParallelProgressBar("parallel", []int{1, 2, 3}, func(v int) error {
		if v == 2 {
			panic("boom")
		}
		return nil
	}, WithWorkers(2), progressbarOpt(func(pb *Progressbar) error {
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))

	assert.ErrorIs(t, err, ErrBug)
	assert.Contains(t, err.Error(), "panic: boom")
	var pe *panicError
	assert.True(t, errors.As(err, &pe))
}

func TestNewParallelProgressBarNoTTYStillParallel(t *testing.T) {
	var active int64
	var maxActive int64
	ready := make(chan struct{})
	var once sync.Once
	items := []int{1, 2, 3}
	err := NewParallelProgressBar("parallel", items, func(v int) error {
		curr := atomic.AddInt64(&active, 1)
		for {
			prev := atomic.LoadInt64(&maxActive)
			if curr <= prev || atomic.CompareAndSwapInt64(&maxActive, prev, curr) {
				break
			}
		}
		if curr >= 2 {
			once.Do(func() {
				close(ready)
			})
		}
		select {
		case <-ready:
		case <-time.After(time.Second):
			return errors.New("timeout waiting for parallel start")
		}
		atomic.AddInt64(&active, -1)
		return nil
	}, WithWorkers(2), progressbarOpt(func(pb *Progressbar) error {
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	assert.NoError(t, err)
	assert.True(t, maxActive > 1)
}

func TestNewParallelProgressBarPropagatesCloseError(t *testing.T) {
	closeErr := io.EOF
	err := NewParallelProgressBar("parallel", []int{1}, func(v int) error {
		return nil
	}, progressbarOpt(func(pb *Progressbar) error {
		pb.err = closeErr
		pb.ticks = make(chan time.Time)
		pb.ticker = time.NewTicker(time.Hour)
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return &termIO{
				in:      in,
				out:     &bytes.Buffer{},
				Width:   40,
				Height:  1,
				Restore: func() error { return nil },
			}, nil
		}
		return nil
	}))
	assert.ErrorIs(t, err, closeErr)
}

func TestProgressStateHelpers(t *testing.T) {
	ps := &progressState{
		maxNum:     100,
		currentNum: 60,
	}

	assert.Equal(t, "4s remaining", ps.remainingTime(10))
	assert.Equal(t, "", ps.remainingTime(0))
	assert.Equal(t, "[==========]", ps.filledBarLine(10, 1.2))
}

func TestNewSliceProgressBarStopsOnYieldFalse(t *testing.T) {
	seq := NewSliceProgressBar("label", []int{1, 2, 3}, progressbarOpt(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	count := 0
	seq(func(v int, err error) bool {
		count++
		return false
	})
	if count != 1 {
		t.Fatalf("expected one item, got %d", count)
	}
}

func TestNewSliceProgressBarReturnsError(t *testing.T) {
	seq := NewSliceProgressBar("label", []int{1}, progressbarOpt(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, errors.New("boom")
		}
		return nil
	}))
	var gotErr error
	seq(func(v int, err error) bool {
		gotErr = err
		return false
	})
	if gotErr == nil {
		t.Fatalf("expected error")
	}
}

func TestProgressbarCloseNoIO(t *testing.T) {
	p := &Progressbar{}
	if err := p.Close(); err != nil {
		t.Fatalf("expected nil error")
	}
}

func TestProgressStateIsDone(t *testing.T) {
	p := &progressState{maxNum: 0, currentNum: 0}
	if p.isDone() {
		t.Fatalf("expected not done")
	}
	p.maxNum = 2
	p.currentNum = 2
	if !p.isDone() {
		t.Fatalf("expected done")
	}
}

func TestProgressbarCloseWithIO(t *testing.T) {
	p := &Progressbar{io: &termIO{}, cancel: func() {}, err: io.EOF}
	err := p.Close()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestProgressbarCloseWaitsForBackgroundStop(t *testing.T) {
	restoreDelay := 50 * time.Millisecond
	p, err := newStartedProgressBar("sync", 1, progressbarOpt(func(pb *Progressbar) error {
		pb.ticks = make(chan time.Time)
		pb.ticker = time.NewTicker(time.Hour)
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return &termIO{
				in:     in,
				out:    &bytes.Buffer{},
				Width:  30,
				Height: 1,
				Restore: func() error {
					time.Sleep(restoreDelay)
					return nil
				},
			}, nil
		}
		return nil
	}))
	assert.NoError(t, err)
	start := time.Now()
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- p.Close()
	}()
	select {
	case err = <-closeDone:
		t.Fatalf("expected close to block for cleanup, got %v", err)
	case <-time.After(restoreDelay / 4):
	}
	select {
	case err = <-closeDone:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for close")
	}
	assert.True(t, time.Since(start) >= restoreDelay)
}

func TestProgressbarAddContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p := &Progressbar{
		config:     config{ctx: ctx},
		io:         &termIO{},
		increments: nil,
	}
	p.Add(1)
}

type closeReader struct {
	closed bool
}

func (c *closeReader) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (c *closeReader) Close() error {
	c.closed = true
	return nil
}

func TestWrapReaderCloseCallsUnderlying(t *testing.T) {
	cr := &closeReader{}
	w := &wrapReader{r: cr, p: &Progressbar{}}
	err := w.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cr.closed {
		t.Fatalf("expected close to be called")
	}
}

func TestWrapReaderCloseProgressError(t *testing.T) {
	cr := &closeReader{}
	p := &Progressbar{io: &termIO{}, cancel: func() {}, err: io.EOF}
	w := &wrapReader{r: cr, p: p}
	err := w.Close()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	if cr.closed {
		t.Fatalf("unexpected close")
	}
}

func TestProgressbarTickShowsElapsed(t *testing.T) {
	p := &Progressbar{
		progressState: progressState{
			maxNum:      10,
			currentNum:  5,
			showElapsed: true,
			startedAt:   time.Now().Add(-2 * time.Second),
			redrawAt:    time.Now().Add(-1 * time.Second),
		},
		label: "test",
		io: &termIO{
			out:     &bytes.Buffer{},
			Width:   40,
			Height:  1,
			Restore: func() error { return nil },
		},
		now: func() time.Time { return time.Now() },
	}
	frame := &bytes.Buffer{}
	if p.tick(frame) {
		t.Fatalf("unexpected done")
	}
}

type errWriterPB struct{}

func (errWriterPB) Write(p []byte) (int, error) {
	return 0, io.EOF
}

func TestProgressbarTickWriteError(t *testing.T) {
	p := &Progressbar{
		progressState: progressState{
			maxNum:     10,
			currentNum: 5,
			redrawAt:   time.Now(),
			startedAt:  time.Now().Add(-time.Second),
		},
		label: "test",
		io: &termIO{
			out:     errWriterPB{},
			Width:   40,
			Height:  1,
			Restore: func() error { return nil },
		},
		now: func() time.Time { return time.Now() },
	}
	frame := &bytes.Buffer{}
	if !p.tick(frame) {
		t.Fatalf("expected done")
	}
	if p.err == nil {
		t.Fatalf("expected error")
	}
}

func TestProgressbarStopRestoreError(t *testing.T) {
	p := &Progressbar{
		rendered: true,
		io: &termIO{
			out:     &bytes.Buffer{},
			Restore: func() error { return io.EOF },
		},
		ticker: time.NewTicker(time.Hour),
	}
	err := p.stop()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}
