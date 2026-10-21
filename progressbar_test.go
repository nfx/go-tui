// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestProgressbarLinesToClearShrinks(t *testing.T) {
	p := &Progressbar{
		rowWidths: []int{80},
	}
	assert.Equal(t, 2, p.linesToClear(40))
	assert.Equal(t, 1, p.linesToClear(0))
	// every row of a stacked frame rewraps on its own
	p.rowWidths = []int{39, 25}
	assert.Equal(t, 2, p.linesToClear(40))
	assert.Equal(t, 4, p.linesToClear(20))
	assert.Equal(t, 2, p.linesToClear(0))
	// nothing drawn yet still clears the cursor row
	p.rowWidths = nil
	assert.Equal(t, 1, p.linesToClear(40))
}

// row renders p in width columns on a terminal of unknown height, joining
// the rows of a stacked frame with a line break.
func (p *Progressbar) row(width int, now time.Time) (text, error) {
	rows, err := p.rows(width, 0, now)
	if err != nil {
		return nil, err
	}
	var out text
	for i, r := range rows {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, r...)
	}
	return out, nil
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

	p, err := newStartedProgressBar(t.Context(), "download", 20,
		WithInput(cio),
		WithOutput(cio),
		opT(func(pb *Progressbar) error {
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

	assert.Equal(t, 39, text(output).width())
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

	p, err := newStartedProgressBar(t.Context(), "download", 20,
		WithInput(cio),
		WithOutput(cio),
		opT(func(pb *Progressbar) error {
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
	// a terminal one row high has no room for the hint row, so the metrics
	// stay on the row of the bar
	assert.Contains(t, stripSGR(output), "download  50%  ╸ 10.00/s · 1s remaining")

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
	assert.Contains(t, stripSGR(second), "download  75% ")
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

	p, err := newStartedProgressBar(t.Context(), "download", 20,
		WithInput(cio),
		WithOutput(cio),
		opT(func(pb *Progressbar) error {
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
	assert.Equal(t, int64(1), metrics.Remaining)
	assert.Equal(t, int64(1), metrics.Elapsed)

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

	p, err := newStartedProgressBar(t.Context(), "sync", 2,
		WithInput(cio),
		WithOutput(cio),
		opT(func(pb *Progressbar) error {
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
	p, err := NewMaxProgressBar(t.Context(), "max", 3)
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, p.Close()) })
	assert.NotNil(t, p)
	assert.Equal(t, int64(3), p.maxNum)

	// Add should be a no-op without a TTY.
	p.Add(3)
}

func TestNewMaxProgressBarAppliesOptions(t *testing.T) {
	_, err := NewMaxProgressBar(t.Context(), "max", 1, opT(func(p *Progressbar) error {
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

	r, err := NewFileProgressReader(t.Context(), &statReader{data: []byte("hello world")}, "file",
		WithInput(cio),
		WithOutput(cio),
		opT(func(pb *Progressbar) error {
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
	_, err = io.ReadAll(r)
	assert.NoError(t, err)
	// Close waits for the progressbar goroutine to stop, so currentNum is safe to touch afterwards.
	assert.NoError(t, r.Close())
	for {
		select {
		case n := <-r.p.increments:
			r.p.currentNum += n
			continue
		default:
		}
		break
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
	_, err := NewFileProgressReader(t.Context(), bytes.NewBufferString("no size"), "file")
	assert.ErrorIs(t, err, errNoSize)
}

type progressSizedReader struct {
	io.Reader
	size int64
}

func (r *progressSizedReader) Size() int64 { return r.size }

func TestFileProgressReaderLogsWithoutTTY(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  string
		size  int64
		chunk int
		want  []int
	}{
		{"each decile", strings.Repeat("x", 100), 100, 1, []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}},
		{"large reads", strings.Repeat("x", 100), 100, 55, []int{50, 100}},
		{"single read", strings.Repeat("x", 100), 100, 100, []int{100}},
		{"empty file", "", 0, 1, []int{100}},
		{"truncated file", strings.Repeat("x", 20), 100, 10, []int{10, 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs, out bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			r, err := NewFileProgressReader(t.Context(), &progressSizedReader{
				Reader: strings.NewReader(tc.data), size: tc.size,
			}, "Download", WithOutput(&out))
			assert.NoError(t, err)
			defer func() { assert.NoError(t, r.Close()) }()
			var data bytes.Buffer
			buf := make([]byte, tc.chunk)
			for {
				n, readErr := r.Read(buf)
				_, err = data.Write(buf[:n])
				assert.NoError(t, err)
				if errors.Is(readErr, io.EOF) {
					break
				}
				assert.NoError(t, readErr)
			}
			_, err = r.Read(buf)
			assert.ErrorIs(t, err, io.EOF)
			assert.NoError(t, r.Close())
			assert.ErrorIs(t, r.p.ctx.Err(), context.Canceled)
			assert.Equal(t, tc.data, data.String())
			assert.Equal(t, "", out.String())
			var percentages []int
			decoder := json.NewDecoder(&logs)
			for decoder.More() {
				var entry struct {
					Level   string `json:"level"`
					Label   string `json:"label"`
					Percent int    `json:"percent"`
				}
				assert.NoError(t, decoder.Decode(&entry))
				assert.Equal(t, "INFO", entry.Level)
				assert.Equal(t, "Download", entry.Label)
				percentages = append(percentages, entry.Percent)
			}
			assert.Equal(t, tc.want, percentages)
		})
	}
}

func TestFileProgressReaderReturnsTerminalErrors(t *testing.T) {
	_, err := NewFileProgressReader(t.Context(), strings.NewReader("data"), "Download",
		opT(func(p *Progressbar) error {
			p.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) {
				return nil, io.ErrClosedPipe
			}
			return nil
		}),
	)
	assert.ErrorIs(t, err, io.ErrClosedPipe)
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
	seq := NewSliceProgressBar(t.Context(), "items", items,
		WithInput(cio),
		WithOutput(cio),
		opT(func(pb *Progressbar) error {
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
	p := newProgressbar(t.Context())
	err := WithFormatRate(func(_ float64) string { return "rate" })(p)
	assert.NoError(t, err)
	if p.fmtRate == nil {
		t.Fatalf("expected format rate function")
	}
}

func TestWithWorkersRejectsNonPositive(t *testing.T) {
	p := newProgressbar(t.Context())
	err := WithWorkers(0)(p)
	assert.ErrorIs(t, err, ErrInvalidState)
}

func TestWithWorkersSetsConfiguredValue(t *testing.T) {
	p := newProgressbar(t.Context())
	err := WithWorkers(7)(p)
	assert.NoError(t, err)
	assert.Equal(t, 7, p.workers)
}

func TestNewParallelProgressBarRunsConcurrently(t *testing.T) {
	tests := []struct {
		name    string
		items   []int
		workers int
	}{
		{name: "three workers", items: []int{1, 2, 3, 4, 5, 6}, workers: 3},
		{name: "no tty", items: []int{1, 2, 3}, workers: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var active, maxActive int64
			ready := make(chan struct{})
			var once sync.Once
			err := NewParallelProgressBar(t.Context(), "parallel", tt.items, func(v int) error {
				curr := atomic.AddInt64(&active, 1)
				for {
					prev := atomic.LoadInt64(&maxActive)
					if curr <= prev || atomic.CompareAndSwapInt64(&maxActive, prev, curr) {
						break
					}
				}
				if curr >= int64(tt.workers) {
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
			}, WithWorkers(tt.workers), opT(func(pb *Progressbar) error {
				pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
					return nil, ErrNoTTY
				}
				return nil
			}))
			assert.NoError(t, err)
			assert.True(t, maxActive > 1)
		})
	}
}

func TestNewParallelProgressBarStopsOnParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	var once sync.Once
	var ran atomic.Int64
	go func() {
		<-started
		cancel()
	}()
	err := NewParallelProgressBar(ctx, "parallel", make([]int, 100), func(int) error {
		ran.Add(1)
		once.Do(func() { close(started) })
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
		return nil
	}, WithWorkers(2), opT(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, ran.Load() < 100)
}

func TestNewParallelProgressBarUsesDefaultNumCPU(t *testing.T) {
	orig := runtimeNumCPU
	runtimeNumCPU = func() int { return 4 }
	t.Cleanup(func() {
		runtimeNumCPU = orig
	})
	p := newProgressbar(t.Context())
	assert.Equal(t, 4, p.workers)
	items := []int{1, 2, 3, 4}
	var active int64
	var maxActive int64
	ready := make(chan struct{})
	var once sync.Once
	err := NewParallelProgressBar(t.Context(), "parallel", items, func(v int) error {
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
	}, opT(func(pb *Progressbar) error {
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
	err := NewParallelProgressBar(t.Context(), "parallel", items, func(v int) error {
		atomic.AddInt64(&processed, 1)
		if v == 7 {
			return fail
		}
		time.Sleep(10 * time.Millisecond)
		return nil
	}, WithWorkers(4), opT(func(pb *Progressbar) error {
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return nil, ErrNoTTY
		}
		return nil
	}))
	assert.ErrorIs(t, err, fail)
	assert.True(t, processed < int64(len(items)))
}

func TestNewParallelProgressBarRecoversPanicsAsBug(t *testing.T) {
	err := NewParallelProgressBar(t.Context(), "parallel", []int{1, 2, 3}, func(v int) error {
		if v == 2 {
			panic("boom")
		}
		return nil
	}, WithWorkers(2), opT(func(pb *Progressbar) error {
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

func TestNewParallelProgressBarPropagatesCloseError(t *testing.T) {
	closeErr := io.EOF
	err := NewParallelProgressBar(t.Context(), "parallel", []int{1}, func(v int) error {
		return nil
	}, opT(func(pb *Progressbar) error {
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

func TestNewSliceProgressBarStopsOnYieldFalse(t *testing.T) {
	seq := NewSliceProgressBar(t.Context(), "label", []int{1, 2, 3}, opT(func(p *Progressbar) error {
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
	seq := NewSliceProgressBar(t.Context(), "label", []int{1}, opT(func(p *Progressbar) error {
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
	p := &Progressbar{io: &termIO{}, cancel: func(error) {}, err: io.EOF}
	err := p.Close()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestProgressbarCloseWaitsForBackgroundStop(t *testing.T) {
	restoreDelay := 50 * time.Millisecond
	p, err := newStartedProgressBar(t.Context(), "sync", 1, opT(func(pb *Progressbar) error {
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
	p := &Progressbar{io: &termIO{}, cancel: func(error) {}, err: io.EOF}
	w := &wrapReader{r: cr, p: p}
	err := w.Close()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	if !cr.closed {
		t.Fatalf("expected underlying close despite progress error")
	}
}

// withLook binds the installed theme to p, as a started bar does.
func withLook(t *testing.T, p *Progressbar) *Progressbar {
	t.Helper()
	p.theme = defaultIO.currentTheme()
	assert.NoError(t, p.bindLook(t.Context()))
	return p
}

func TestProgressbarTickShowsElapsed(t *testing.T) {
	p := withLook(t, &Progressbar{
		progressState: progressState{
			maxNum:      10,
			currentNum:  5,
			showElapsed: true,
			startedAt:   time.Now().Add(-2 * time.Second),
			redrawAt:    time.Now().Add(-1 * time.Second),
		},
		label: text("test"),
		io: &termIO{
			out:     &bytes.Buffer{},
			Width:   40,
			Height:  1,
			Restore: func() error { return nil },
		},
		now: func() time.Time { return time.Now() },
	})
	frame := &bytes.Buffer{}
	if p.tick(frame) {
		t.Fatalf("unexpected done")
	}
	out, ok := p.io.out.(*bytes.Buffer)
	assert.True(t, ok)
	assert.Contains(t, stripSGR(out.String()), "2s elapsed")
}

type errWriterPB struct{}

func (errWriterPB) Write(p []byte) (int, error) {
	return 0, io.EOF
}

func TestProgressbarTickWriteError(t *testing.T) {
	p := withLook(t, &Progressbar{
		progressState: progressState{
			maxNum:     10,
			currentNum: 5,
			redrawAt:   time.Now(),
			startedAt:  time.Now().Add(-time.Second),
		},
		label: text("test"),
		io: &termIO{
			out:     errWriterPB{},
			Width:   40,
			Height:  1,
			Restore: func() error { return nil },
		},
		now: func() time.Time { return time.Now() },
	})
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

func TestMetricsSnapshotZeroMax(t *testing.T) {
	for _, current := range []int64{0, 5} {
		p := &progressState{currentNum: current}
		got := p.metricsSnapshot().Complete
		if got != 0 {
			t.Fatalf("current=%d: Complete = %v, want 0", current, got)
		}
	}
}

func TestProgressBarWithContextCancelBound(t *testing.T) {
	parentCtx := t.Context()
	p, err := newStartedProgressBar(parentCtx, "test", 10,
		opT(func(pb *Progressbar) error {
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
		}),
	)
	assert.NoError(t, err)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- p.Close()
	}()

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("p.Close() timed out because p.cancel was not bound to configured context")
	}

	select {
	case <-p.ctx.Done():
	default:
		t.Fatal("expected p.ctx to be canceled after Close()")
	}
}

func TestNewFileProgressReaderWithContextCancelBound(t *testing.T) {
	parentCtx := t.Context()
	r := bytes.NewReader([]byte("test data"))
	w, err := NewFileProgressReader(parentCtx, r, "test",
		opT(func(pb *Progressbar) error {
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
		}),
	)
	assert.NoError(t, err)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- w.Close()
	}()

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("w.Close() timed out because p.cancel was not bound to configured context")
	}

	select {
	case <-w.p.ctx.Done():
	default:
		t.Fatal("expected w.p.ctx to be canceled after Close()")
	}
}

func TestProgressStateIncrementCalculatesElapsedFromNow(t *testing.T) {
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	p := &progressState{
		maxNum:     10,
		currentNum: 5,
		startedAt:  start,
		redrawAt:   start.Add(time.Second),
	}
	now := start.Add(3 * time.Second)
	p.increment(now)
	assert.Equal(t, 3*time.Second, p.elapsed)
	assert.Equal(t, int64(3), p.metricsSnapshot().Elapsed)
}

func TestProgressbarAddAfterStoppedDoesNotBlock(t *testing.T) {
	stopped := make(chan struct{})
	close(stopped)
	p := &Progressbar{
		io:         &termIO{},
		ctx:        t.Context(),
		stopped:    stopped,
		increments: make(chan int64),
	}
	done := make(chan struct{})
	go func() { p.Add(1); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Add blocked after renderer stopped")
	}
}

// themedBar is a bar of the given state rendering through th to a plain
// destination, for driving row directly.
func themedBar(t *testing.T, th Theme, state progressState) *Progressbar {
	t.Helper()
	c, err := th.compile()
	assert.NoError(t, err)
	p := &Progressbar{progressState: state, label: text("build"), theme: c}
	p.look, err = p.bind(c.bind(profilePlain, background{}))
	assert.NoError(t, err)
	return p
}

// compactTheme is the ASCII theme with the single-row progress layout.
func compactTheme() Theme {
	th := asciiTheme(false)
	th.Progress.Compact = true
	return th
}

// halfDone is a bar halfway to 100 at 50 per second, one second in, with
// every metric shown.
func halfDone(now time.Time) progressState {
	return progressState{
		maxNum:         100,
		currentNum:     50,
		sinceRedrawNum: 50,
		startedAt:      now.Add(-time.Second),
		rollingRates:   []float64{50},
		showRate:       true,
		showEstimate:   true,
		showElapsed:    true,
	}
}

func TestThemedProgressRowDropsMetricsToFit(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	bar := func(fill, head, empty int) string {
		return strings.Repeat("=", fill) + strings.Repeat(">", head) + strings.Repeat("-", empty)
	}
	const all, two, one = " 50.00/s · 1s remaining · 1s elapsed", " 50.00/s · 1s remaining", " 50.00/s"
	for _, tc := range []struct {
		width int
		want  string
	}{
		{60, "build 50%  " + bar(6, 1, 6) + all},
		{48, "build 50%  " + bar(0, 1, 0) + all},
		{47, "build 50%  " + bar(6, 1, 6) + two},
		{35, "build 50%  " + bar(0, 1, 0) + two},
		{34, "build 50%  " + bar(7, 1, 7) + one},
		{20, "build 50%  " + bar(0, 1, 0) + one},
		{19, "build 50%  " + bar(4, 0, 4)},
		{12, "build 50%  " + bar(0, 1, 0)},
		{11, "build 50%  "},
		{8, "build 5…"},
		{0, ""},
		{-1, ""},
	} {
		p := themedBar(t, compactTheme(), halfDone(now))
		row, err := p.row(tc.width, now)
		assert.NoError(t, err)
		assert.Equal(t, tc.want, row.String())
		assert.True(t, row.width() <= max(tc.width, 0))
	}
}

func TestThemedProgressASCIISymbols(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	p := themedBar(t, asciiTheme(false), halfDone(now))
	row, err := p.row(47, now)
	assert.NoError(t, err)
	// the elapsed time does not fit the hint row, which drops it whole
	assert.Equal(t, "build  50%  =================>-----------------\n"+
		"50 / 100 items | 50.00 items/s | 1s left", row.String())
	p = themedBar(t, compactTheme(), halfDone(now))
	row, err = p.row(47, now)
	assert.NoError(t, err)
	assert.Equal(t, "build 50%  ======>------ 50.00/s · 1s remaining", row.String())
}

func TestThemedProgressBarWidthBounds(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	th := compactTheme()
	th.Progress.MaxBarWidth = 4
	p := themedBar(t, th, halfDone(now))
	row, err := p.row(80, now)
	assert.NoError(t, err)
	assert.Equal(t, "build 50%  ==-- 50.00/s · 1s remaining · 1s elapsed", row.String())

	th = compactTheme()
	th.Progress.MinBarWidth = 10
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(47, now)
	assert.NoError(t, err)
	assert.Equal(t, "build 50%  ======>------ 50.00/s · 1s remaining", row.String())
	// a 6-column bar is below the minimum, so the remaining time goes
	row, err = p.row(40, now)
	assert.NoError(t, err)
	assert.Equal(t, "build 50%  ==========>---------- 50.00/s", row.String())

	// the stacked bar is bounded the same way; the hint has its own row
	th = asciiTheme(false)
	th.Progress.MaxBarWidth = 4
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(80, now)
	assert.NoError(t, err)
	assert.Equal(t, "build  50%  ==--\n50 / 100 items | 50.00 items/s | 1s left | 1s elapsed", row.String())
}

func TestThemedProgressHeadMarksPartialCell(t *testing.T) {
	l := &progressLook{style: progressStyle, symbols: ASCIISymbols()}
	l.symbols.BarEmpty = "."
	for _, tc := range []struct {
		completion float64
		want       string
	}{
		{0, ".........."},
		{0.01, ">........."},
		{0.5, "=====....."},
		{0.55, "=====>...."},
		{0.99, "=========>"},
		{1, "=========="},
		{1.5, "=========="},
		{-0.5, ".........."},
	} {
		assert.Equal(t, tc.want, l.bar(10, tc.completion).String())
	}
	assert.Equal(t, "", l.bar(0, 0.5).String())
}

func TestThemedProgressUnknownAndExceededTotals(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		edit             func(*progressState)
		width            int
		stacked, compact string
	}{{
		// no total: an unknown percent, no estimate, an empty track
		"unknown total", func(s *progressState) { s.maxNum = 0 }, 30,
		"build  ?    ------------------\n50.00 items/s",
		"build ?    ----------- 50.00/s",
	}, {
		// past the total: full bar, nothing remaining, which is not unknown
		"exceeded total", func(s *progressState) { s.currentNum, s.sinceRedrawNum = 150, 150 }, 40,
		"build  100% ============================\n150 / 100 items | 50.00 items/s",
		"build 100% ====== 50.00/s · 0s remaining",
	}, {
		// no rate yet: the estimate is unknown, not zero
		"no rate", func(s *progressState) { s.rollingRates = nil }, 30,
		"build  50%  =========---------\n50 / 100 items | 0.00 items/s",
		"build 50%  ======------ 0.00/s",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			state := halfDone(now)
			state.showElapsed = false
			tc.edit(&state)
			row, err := themedBar(t, asciiTheme(false), state).row(tc.width, now)
			assert.NoError(t, err)
			assert.Equal(t, tc.stacked, row.String())
			row, err = themedBar(t, compactTheme(), state).row(tc.width, now)
			assert.NoError(t, err)
			assert.Equal(t, tc.compact, row.String())
		})
	}
}

func TestThemedProgressView(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	th := compactTheme()
	th.Templates.Progress.Label = `<{{ .Label }}>`
	th.Templates.Progress.Metrics = `{{ .Current }}/{{ .Total }}{{ with .Rate }} at {{ . }}{{ end }}` +
		`{{ with .Remaining }} eta {{ . }}{{ end }}{{ with .Elapsed }} after {{ . }}{{ end }}`
	th.Templates.Progress.Frame = `{{ .Label }} {{ .Percent }} [{{ .Bar }}] {{ .Metrics }} {{ len .Values }}`
	state := halfDone(now)
	state.fmtRate = func(r float64) string { return fmt.Sprintf("%.0f\nitems", r) }
	p := themedBar(t, th, state)
	p.label = text("multi\nline\x1b[2K")
	row, err := p.row(80, now)
	assert.NoError(t, err)
	// the rate is formatted by WithFormatRate and kept on one line
	assert.Equal(
		t,
		"<multi line> 50 ["+strings.Repeat(
			"=",
			12,
		)+">"+strings.Repeat(
			"-",
			12,
		)+"] 50/100 at 50 items eta 1s after 1s 3",
		row.String(),
	)
	// the metrics slot sees only the metrics that fit
	row, err = p.row(50, now)
	assert.NoError(t, err)
	assert.Equal(t, "<multi line> 50 [==--] 50/100 at 50 items eta 1s 2", row.String())

	// a stacked frame gets no metrics; the hint row gets the pairs that fit
	th.Progress.Compact = false
	th.Templates.Progress.Hint = `{{ range . }}[{{ .Left }}{{ with .Right }} {{ . }}{{ end }}]{{ end }}`
	th.Templates.Progress.Frame = `{{ .Label }} {{ .Percent }} [{{ .Bar }}] {{ len .Hints }}/{{ len .Values }}{{ .Metrics }}`
	p = themedBar(t, th, state)
	p.label = text("multi\nline\x1b[2K")
	row, err = p.row(40, now)
	assert.NoError(t, err)
	assert.Equal(t, "<multi line> 50 [=========---------] 3/2\n[50 / 100 items][50 items /s][1s left]", row.String())
}

func TestThemedProgressHiddenSlots(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	th := compactTheme()
	th.Templates.Progress.Label = Hidden
	th.Templates.Progress.Metrics = Hidden
	p := themedBar(t, th, halfDone(now))
	row, err := p.row(16, now)
	assert.NoError(t, err)
	assert.Equal(t, "50%  =====>-----", row.String())

	// hidden metrics leave the stacked hint alone, and the other way round
	th.Progress.Compact = false
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(16, now)
	assert.NoError(t, err)
	assert.Equal(t, "50%  =====>-----\n50 / 100 items", row.String())
	th.Templates.Progress.Hint = Hidden
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(16, now)
	assert.NoError(t, err)
	assert.Equal(t, "50%  =====>-----", row.String())
	th.Templates.Progress.Metrics = ""
	th.Progress.Compact = true
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(40, now)
	assert.NoError(t, err)
	assert.Equal(t, "50%  ======------ 50.00/s · 1s remaining", row.String())
}

func TestThemedProgressFrameErrors(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	for _, tc := range []struct {
		funcs template.FuncMap
		frame Template
		want  string
	}{
		{
			template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }},
			`{{ boom }}{{ .Bar }}`, "theme.Templates.Progress.Frame: template: Progress.Frame:1:3: executing",
		},
		{
			template.FuncMap{"raw": func(v any) string { return fmt.Sprint(v) }},
			`{{ .Bar }}{{ raw . }}`, "theme.Templates.Progress.Frame: want the bar once",
		},
		{
			template.FuncMap{"erase": func() string { return "\x1b[2K" }},
			`{{ erase }}{{ .Bar }}`, `theme.Templates.Progress.Frame: "\x1b[2K": want text and SGR only`,
		},
	} {
		th := NewTheme()
		th.Funcs = tc.funcs
		th.Templates.Progress.Frame = tc.frame
		p := themedBar(t, th, halfDone(now))
		_, err := p.row(40, now)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestThemedProgressRoles(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	th := NewTheme()
	th.Light = true
	c, err := th.compile()
	assert.NoError(t, err)
	look := c.bind(profileTrueColor, background{light: true})
	roles := look.roles
	assert.True(t, len(roles.accent) > 0 && len(roles.guide) > 0 && len(roles.muted) > 0 && len(roles.foreground) > 0)
	p := &Progressbar{progressState: halfDone(now), label: text("build"), theme: c}
	p.look, err = p.bind(look)
	assert.NoError(t, err)
	rows, err := p.rows(60, 0, now)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(rows))
	bar, hint := rows[0].String(), rows[1].String()
	assert.Contains(t, bar, string(roles.foreground)+"build")
	assert.Contains(t, bar, string(roles.accent)+"50% ")
	assert.Contains(t, bar, string(roles.accent)+strings.Repeat("━", 24)+fgDefault+
		string(roles.guide)+strings.Repeat("─", 24)+fgDefault)
	// the hint sets the values apart from their units and separators
	assert.Contains(t, hint, string(roles.accent)+"50 / 100"+fgDefault+" "+string(roles.guide)+"items"+fgDefault)
	assert.Contains(t, hint, string(roles.guide)+" · "+fgDefault+string(roles.accent)+"50.00"+fgDefault)
	assert.Equal(t, "50 / 100 items · 50.00 items/s · 1s left · 1s elapsed", stripSGR(hint))
	// running work is not a success or a failure
	for _, row := range rows {
		out := row.String()
		assert.True(t, !strings.Contains(out, string(roles.success)) && !strings.Contains(out, string(roles.failure)))
	}
	assert.Equal(t, 60, rows[0].width())
}

// fakeTerminal is a terminal for a bar, with manual ticks. tick renders at
// the given time since the start; each write is one receive from frames.
// restored reports a released terminal.
type fakeTerminal struct {
	frames   frameWriter
	tick     func(time.Duration)
	restored *atomic.Bool
	terminal opt
}

// newFakeTerminal is a terminal width columns wide.
func newFakeTerminal(t *testing.T, width int) fakeTerminal {
	t.Helper()
	ft := fakeTerminal{frames: make(frameWriter), restored: &atomic.Bool{}}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	clock := &atomic.Int64{}
	ft.terminal = opT(func(pb *Progressbar) error {
		pb.now = func() time.Time { return start.Add(time.Duration(clock.Load())) }
		pb.ticks = ticks
		pb.makeTermIO = func(in io.Reader, _ io.Writer) (*termIO, error) {
			return &termIO{in: in, out: ft.frames, Width: width, Height: 10, Restore: func() error {
				ft.restored.Store(true)
				return nil
			}}, nil
		}
		return nil
	})
	ft.tick = func(at time.Duration) {
		t.Helper()
		clock.Store(int64(at))
		select {
		case ticks <- time.Time{}:
		case <-time.After(time.Second):
			t.Fatal("tick not delivered")
		}
	}
	return ft
}

// options make a bar write to the terminal.
func (ft fakeTerminal) options() []opt {
	return []opt{WithOutput(ft.frames), ft.terminal}
}

// closeOnCleanup closes p after the test, receiving the final clear of a bar
// the test left open.
func (ft fakeTerminal) closeOnCleanup(t *testing.T, p *Progressbar) {
	t.Helper()
	t.Cleanup(func() {
		go func() {
			for range ft.frames {
			}
		}()
		_ = p.Close() //nolint:errcheck // test cleanup; tests check their own errors
		close(ft.frames)
	})
}

// themedProgress is a bar towards size rendering through th on a terminal
// 40 columns wide.
func themedProgress(
	t *testing.T,
	th Theme,
	size int64,
) (*Progressbar, frameWriter, func(time.Duration), *atomic.Bool) {
	t.Helper()
	useTheme(t, th)
	ft := newFakeTerminal(t, 40)
	p, err := NewMaxProgressBar(t.Context(), "build", size, ft.options()...)
	assert.NoError(t, err)
	ft.closeOnCleanup(t, p)
	return p, ft.frames, ft.tick, ft.restored
}

// untilProgressStopped returns everything the bar writes until its loop
// ends, failing the test unless it ends within a second.
func untilProgressStopped(t *testing.T, p *Progressbar, frames frameWriter) string {
	t.Helper()
	var out strings.Builder
	timeout := time.After(time.Second)
	for {
		select {
		case f := <-frames:
			out.WriteString(f)
		case <-p.stopped:
			return out.String()
		case <-timeout:
			t.Fatal("progress bar did not stop")
		}
	}
}

// frameOf is a frame of rows as the bar writes it.
func frameOf(rows ...string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString("\r" + r + "\n")
	}
	return b.String() + "\r"
}

// erased is what erases n terminal rows above the cursor.
func erased(t *testing.T, n int) string {
	t.Helper()
	var b bytes.Buffer
	assert.NoError(t, new(termIO).clear(n, &b))
	return b.String()
}

func TestThemedProgressCompletes(t *testing.T) {
	p, frames, tick, restored := themedProgress(t, asciiTheme(false), 100)
	p.Add(50)
	tick(time.Second)
	// the estimate does not fit the hint row, which drops it whole
	assert.Equal(t, frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"50 / 100 items | 50.00 items/s"), <-frames)
	p.Add(50)
	tick(2 * time.Second)
	assert.Equal(t, erased(t, 2)+frameOf("build  100% "+strings.Repeat("=", 27),
		"100 / 100 items | 50.00 items/s"), <-frames)
	// done: both rows are erased, and no completion line is retained
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, p, frames))
	assert.True(t, restored.Load())
	assert.NoError(t, p.Close())
}

func TestThemedProgressCloseBeforeDone(t *testing.T) {
	p, frames, tick, restored := themedProgress(t, asciiTheme(false), 100)
	p.Add(5)
	tick(time.Second)
	assert.Equal(t, frameOf("build  5%   =>"+strings.Repeat("-", 25), "5 / 100 items | 0.00 items/s"), <-frames)
	closed := make(chan error)
	go func() { closed <- p.Close() }()
	// stopping looks like completing: the rows are erased
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, p, frames))
	assert.NoError(t, <-closed)
	assert.True(t, restored.Load())
}

func TestThemedProgressResize(t *testing.T) {
	p, frames, tick, _ := themedProgress(t, asciiTheme(false), 100)
	p.Add(50)
	tick(time.Second)
	assert.Equal(t, frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"50 / 100 items | 50.00 items/s"), <-frames)
	// the 39-column bar row and the 30-column hint row each wrap on 20
	// columns, so four rows are cleared
	p.io.Width = 20
	tick(2 * time.Second)
	assert.Equal(t, erased(t, 4)+frameOf("build  50%  ===>---", "50 / 100 items"), <-frames)
	// the hint row goes once nothing fits
	p.io.Width = 14
	tick(3 * time.Second)
	assert.Equal(t, erased(t, 3)+frameOf("build  50%  >"), <-frames)
	p.io.Width = 40
	tick(4 * time.Second)
	assert.Equal(t, erased(t, 1)+frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"50 / 100 items | 50.00 items/s"), <-frames)
}

func TestThemedProgressShortTerminal(t *testing.T) {
	p, frames, tick, _ := themedProgress(t, asciiTheme(false), 100)
	p.Add(50)
	// one row has no room for the hint, so the metrics join the bar row
	p.io.Height = 1
	tick(time.Second)
	assert.Equal(t, frameOf("build  50%  ==-- 50.00/s · 1s remaining"), <-frames)
	// nor do two, since the cursor takes the row below the frame
	p.io.Height = 2
	tick(time.Second)
	assert.Equal(t, erased(t, 1)+frameOf("build  50%  ==-- 50.00/s · 1s remaining"), <-frames)
	// a taller terminal gets the hint row back
	p.io.Height = 3
	tick(2 * time.Second)
	assert.Equal(t, erased(t, 1)+frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"50 / 100 items | 50.00 items/s"), <-frames)
}

func TestThemedProgressLongLabel(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	for _, tc := range []struct {
		width int
		want  string
	}{
		// the label is shortened, by columns, before the bar goes
		{40, "ファイル build  50%  =========>---------"},
		{30, "ファイル build  50%  ====>----"},
		{24, "ファイル build  50%  =>-"},
		{20, "ファイル bu…  50%  >"},
		{17, "ファイル…  50%  >"},
		// a wide character that does not fit leaves its column to the bar
		{14, "ファ…  50%  =-"},
		// too narrow for an ellipsis and a label: the label goes
		{9, "50%  ==--"},
		// too narrow for the bar: the start of the row
		{4, "50%…"},
	} {
		p := themedBar(t, asciiTheme(false), halfDone(now))
		p.label = text("ファイル build")
		p.showRate, p.showEstimate, p.showElapsed = false, false, false
		rows, err := p.rows(tc.width, 0, now)
		assert.NoError(t, err)
		assert.Equal(t, tc.want, rows[0].String())
		assert.True(t, rows[0].width() <= tc.width)
	}
}

func TestThemedProgressRenderErrorEndsBar(t *testing.T) {
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Progress.Label = `{{ if eq .Current 150 }}{{ boom }}{{ end }}{{ .Label }}`
	p, frames, tick, restored := themedProgress(t, th, 200)
	p.Add(100)
	tick(time.Second)
	assert.Equal(t, frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"100 / 200 items | 100.00 items/s"), <-frames)
	p.Add(50)
	tick(2 * time.Second)
	// no partial frame: only the drawn rows are cleared
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, p, frames))
	assert.True(t, restored.Load())
	err := p.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redraw: theme.Templates.Progress.Label")
	assert.Contains(t, err.Error(), "boom")
	// nobody renders anymore, and Add does not block
	p.Add(1)
}

func TestThemedProgressWriteErrorEndsBar(t *testing.T) {
	useTheme(t, NewTheme())
	restored := false
	p, err := NewMaxProgressBar(t.Context(), "build", 10, opT(func(pb *Progressbar) error {
		pb.ticks = time.After(0)
		pb.makeTermIO = func(in io.Reader, _ io.Writer) (*termIO, error) {
			return &termIO{in: in, out: errWriterPB{}, Width: 40, Height: 1, Restore: func() error {
				restored = true
				return nil
			}}, nil
		}
		return nil
	}))
	assert.NoError(t, err)
	<-p.stopped
	err = p.Close()
	assert.ErrorIs(t, err, io.EOF)
	assert.True(t, restored)
}

func TestThemedSliceProgress(t *testing.T) {
	useTheme(t, asciiTheme(false))
	frames := make(frameWriter, 16)
	ticks := make(chan time.Time)
	var clock atomic.Int64
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	items := []string{"a", "b", "c", "d"}
	seq := NewSliceProgressBar(t.Context(), "items", items, opT(func(pb *Progressbar) error {
		pb.now = func() time.Time { return start.Add(time.Duration(clock.Load())) }
		pb.ticks = ticks
		pb.makeTermIO = func(in io.Reader, _ io.Writer) (*termIO, error) {
			return &termIO{in: in, out: frames, Width: 30, Height: 10, Restore: func() error { return nil }}, nil
		}
		return nil
	}))
	var got []string
	for v, err := range seq {
		assert.NoError(t, err)
		got = append(got, v)
		if v == "c" {
			clock.Store(int64(time.Second))
			ticks <- time.Time{}
			// two items done; too few for a rate, so no estimate
			assert.Equal(t, frameOf("items  50%  ========>--------", "2 / 4 items | 0.00 items/s"), <-frames)
		}
	}
	assert.Equal(t, items, got)
	// the iterator closed the bar, which erased its rows
	close(frames)
	var rest strings.Builder
	for f := range frames {
		rest.WriteString(f)
	}
	assert.Equal(t, erased(t, 2), rest.String())
}

func TestThemedSliceProgressReturnsRenderError(t *testing.T) {
	th := NewTheme()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Progress.Hint = `{{ boom }}`
	useTheme(t, th)
	ticks := make(chan time.Time)
	seq := NewSliceProgressBar(t.Context(), "items", []int{1, 2}, opT(func(pb *Progressbar) error {
		pb.ticks = ticks
		pb.makeTermIO = func(in io.Reader, _ io.Writer) (*termIO, error) {
			return &termIO{in: in, out: io.Discard, Width: 30, Height: 10, Restore: func() error { return nil }}, nil
		}
		return nil
	}))
	var errs []error
	for v, err := range seq {
		if v == 1 {
			ticks <- time.Time{}
		}
		errs = append(errs, err)
	}
	assert.Equal(t, 3, len(errs))
	assert.Contains(t, errs[2].Error(), "theme.Templates.Progress.Hint")
}

func TestThemedProgressWithoutTerminal(t *testing.T) {
	th := NewTheme()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Progress.Label = `{{ boom }}`
	th.Templates.Progress.Metrics = `{{ boom }}`
	th.Templates.Progress.Frame = `{{ boom }}{{ .Bar }}`
	useTheme(t, th)
	noTTY := opT(func(pb *Progressbar) error {
		pb.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) { return nil, ErrNoTTY }
		return nil
	})
	p, err := NewMaxProgressBar(t.Context(), "build", 3, noTTY)
	assert.NoError(t, err)
	p.Add(3)
	assert.NoError(t, p.Close())
	var got []int
	for v, err := range NewSliceProgressBar(t.Context(), "items", []int{1, 2}, noTTY) {
		assert.NoError(t, err)
		got = append(got, v)
	}
	assert.Equal(t, []int{1, 2}, got)
	var sum atomic.Int64
	err = NewParallelProgressBar(t.Context(), "items", []int{1, 2, 3}, func(v int) error {
		sum.Add(int64(v))
		return nil
	}, WithWorkers(2), noTTY)
	assert.NoError(t, err)
	assert.Equal(t, int64(6), sum.Load())
}

func TestThemedProgressCapturesTheme(t *testing.T) {
	th := asciiTheme(false)
	th.Symbols.BarFill = "#"
	p, frames, tick, _ := themedProgress(t, th, 100)
	// a theme installed later does not reach the running bar
	later := asciiTheme(false)
	later.Symbols.BarFill = "="
	later.Progress.Compact = true
	assert.NoError(t, defaultIO.installTheme(later))
	p.Add(60)
	tick(time.Second)
	assert.Equal(t, frameOf("build  60%  "+strings.Repeat("#", 16)+">"+strings.Repeat("-", 10),
		"60 / 100 items | 60.00 items/s"), <-frames)
}

// parallelRun is a parallel bar running in the background on a terminal 40
// columns wide; done receives its result.
type parallelRun struct {
	fakeTerminal
	done chan error
}

// runParallel runs yield over items with NewParallelProgressBar under the
// installed theme and gate.
func runParallel(ctx context.Context, t *testing.T, items []int, yield func(int) error, o ...opt) parallelRun {
	t.Helper()
	r := parallelRun{fakeTerminal: newFakeTerminal(t, 40), done: make(chan error, 1)}
	o = append(r.options(), o...)
	go func() { r.done <- NewParallelProgressBar(ctx, "build", items, yield, o...) }()
	return r
}

// wait returns everything written until the run ends, and its result.
func (r parallelRun) wait(t *testing.T) (string, error) {
	t.Helper()
	var out strings.Builder
	timeout := time.After(time.Second)
	for {
		select {
		case f := <-r.frames:
			out.WriteString(f)
		case err := <-r.done:
			return out.String(), err
		case <-timeout:
			t.Fatal("parallel run did not end")
		}
	}
}

// openedWithin blocks a worker until gate closes, failing its item after a
// second, so that a broken test ends instead of hanging.
func openedWithin(gate <-chan struct{}) error {
	select {
	case <-gate:
		return nil
	case <-time.After(time.Second):
		return errors.New("gate not opened")
	}
}

// receiveN receives n values from ch, failing the test after a second.
func receiveN[T any](t *testing.T, ch <-chan T, n int) {
	t.Helper()
	for range n {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("not received")
		}
	}
}

func TestThemedParallelProgressCompletes(t *testing.T) {
	useTheme(t, asciiTheme(false))
	started := make(chan int, 4)
	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})}
	r := runParallel(t.Context(), t, []int{0, 1, 2, 3}, func(v int) error {
		started <- v
		return openedWithin(gates[v])
	}, WithWorkers(2))
	receiveN(t, started, 2)
	close(gates[0])
	close(gates[1])
	// a worker takes its next item only after adding the last one
	receiveN(t, started, 2)
	r.tick(time.Second)
	assert.Equal(t, frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"2 / 4 items | 0.00 items/s"), <-r.frames)
	close(gates[2])
	close(gates[3])
	out, err := r.wait(t)
	assert.NoError(t, err)
	// done: the rows are erased, and no completion line is retained
	assert.Equal(t, erased(t, 2), out)
	assert.True(t, r.restored.Load())
}

func TestThemedParallelProgressSharesOneRow(t *testing.T) {
	th := asciiTheme(false)
	th.Symbols.BarEmpty = "_"
	useTheme(t, th)
	var active, maxActive atomic.Int64
	all := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	r := runParallel(t.Context(), t, []int{0, 1, 2, 3, 4, 5}, func(int) error {
		n := active.Add(1)
		for prev := maxActive.Load(); n > prev && !maxActive.CompareAndSwap(prev, n); prev = maxActive.Load() {
		}
		if n == 3 {
			once.Do(func() { close(all) })
		}
		err := openedWithin(gate)
		active.Add(-1)
		return err
	}, WithWorkers(3))
	receiveN(t, all, 1)
	// a theme installed while the workers run does not reach the bar
	later := asciiTheme(false)
	later.Symbols.BarEmpty = "."
	assert.NoError(t, defaultIO.installTheme(later))
	r.tick(time.Second)
	// three workers, one bar in the theme captured at the start
	assert.Equal(t, frameOf("build  0%   "+strings.Repeat("_", 27), "0 / 6 items | 0.00 items/s"), <-r.frames)
	close(gate)
	out, err := r.wait(t)
	assert.NoError(t, err)
	assert.Equal(t, erased(t, 2), out)
	assert.Equal(t, int64(3), maxActive.Load())
}

func TestThemedParallelProgressFirstFailure(t *testing.T) {
	useTheme(t, asciiTheme(false))
	fail := errors.New("fail")
	started := make(chan int, 3)
	gate0, gate1 := make(chan struct{}), make(chan struct{})
	r := runParallel(t.Context(), t, []int{0, 1, 2}, func(v int) error {
		switch v {
		case 0:
			started <- v
			return openedWithin(gate0)
		case 1:
			started <- v
			if err := openedWithin(gate1); err != nil {
				return err
			}
			return fail
		}
		return nil
	}, WithWorkers(2))
	receiveN(t, started, 2)
	r.tick(time.Second)
	assert.Equal(t, frameOf("build  0%   "+strings.Repeat("-", 27), "0 / 3 items | 0.00 items/s"), <-r.frames)
	close(gate1)
	close(gate0)
	out, err := r.wait(t)
	// the failure alone, and the rows are erased rather than completed
	assert.ErrorIs(t, err, fail)
	assert.Equal(t, "fail", err.Error())
	assert.Equal(t, erased(t, 2), out)
	assert.True(t, r.restored.Load())
}

func TestThemedParallelProgressCancel(t *testing.T) {
	useTheme(t, asciiTheme(false))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan int, 2)
	gate := make(chan struct{})
	r := runParallel(ctx, t, []int{0, 1}, func(v int) error {
		started <- v
		return openedWithin(gate)
	}, WithWorkers(1))
	receiveN(t, started, 1)
	r.tick(time.Second)
	assert.Equal(t, frameOf("build  0%   "+strings.Repeat("-", 27), "0 / 2 items | 0.00 items/s"), <-r.frames)
	cancel()
	close(gate)
	out, err := r.wait(t)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, erased(t, 2), out)
	assert.True(t, r.restored.Load())
}

func TestThemedParallelProgressRenderError(t *testing.T) {
	th := NewTheme()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Progress.Label = `{{ if eq .Current 1 }}{{ boom }}{{ end }}{{ .Label }}`
	useTheme(t, th)
	items := make([]int, 64)
	for i := range items {
		items[i] = i
	}
	var processed atomic.Int64
	second := make(chan struct{})
	gate := make(chan struct{})
	r := runParallel(t.Context(), t, items, func(v int) error {
		processed.Add(1)
		if v == 1 {
			close(second)
			return openedWithin(gate)
		}
		return nil
	}, WithWorkers(1))
	// the only worker added the first item before taking the second
	receiveN(t, second, 1)
	r.tick(time.Second)
	close(gate)
	out, err := r.wait(t)
	// nothing was drawn, so nothing is cleared
	assert.Equal(t, "", out)
	assert.True(t, r.restored.Load())
	// why rendering ended, not that it cancelled the workers
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redraw: theme.Templates.Progress.Label")
	assert.Contains(t, err.Error(), "boom")
	assert.True(t, !errors.Is(err, context.Canceled))
	assert.True(t, processed.Load() < int64(len(items)))
}

func TestByteSize(t *testing.T) {
	for _, tc := range []struct {
		n    float64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1023.6, "1.0 KiB"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1<<20 - 1, "1.0 MiB"},
		{10 << 20, "10.0 MiB"},
		{1.5 * (1 << 30), "1.5 GiB"},
		{1 << 70, "1024.0 EiB"},
	} {
		assert.Equal(t, tc.want, new(Progressbar).byteSize(tc.n))
	}
}

// halfFile is a file halfway through 10 MiB at 1 MiB per second, one
// second in, with every metric shown.
func halfFile(now time.Time) progressState {
	state := halfDone(now)
	state.bytes = true
	state.maxNum, state.currentNum, state.sinceRedrawNum = 10<<20, 5<<20, 5<<20
	state.rollingRates = []float64{1 << 20}
	return state
}

func TestThemedFileProgressByteMetrics(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	const amount, rate = " 5.0 MiB / 10.0 MiB", " · 1.0 MiB/s"
	for _, tc := range []struct {
		width int
		want  string
	}{
		{80, amount + rate + " · 5s remaining · 1s elapsed"},
		{60, amount + rate + " · 5s remaining"},
		{45, amount + rate},
		{35, amount},
		{30, ""},
	} {
		p := themedBar(t, compactTheme(), halfFile(now))
		row, err := p.row(tc.width, now)
		assert.NoError(t, err)
		// the amount is the last metric to go
		assert.Equal(t, tc.want, afterTrack(row))
		assert.Equal(t, tc.width, row.width())
	}

	// without a total, the bytes read alone
	state := halfFile(now)
	state.maxNum, state.showElapsed = 0, false
	p := themedBar(t, compactTheme(), state)
	row, err := p.row(40, now)
	assert.NoError(t, err)
	assert.Equal(t, "build ?    "+strings.Repeat("-", 9)+" 5.0 MiB · 1.0 MiB/s", row.String())

	// WithFormatRate formats the rate of a file too
	state = halfFile(now)
	state.fmtRate = func(r float64) string { return fmt.Sprintf("%.0f bytes", r) }
	th := compactTheme()
	th.Templates.Progress.Metrics = `{{ .Amount }}|{{ .Rate }}|{{ .Current }}/{{ .Total }}`
	p = themedBar(t, th, state)
	row, err = p.row(80, now)
	assert.NoError(t, err)
	assert.Equal(t, " 5.0 MiB / 10.0 MiB|1048576 bytes|5242880/10485760", afterTrack(row))

	// a counted bar has no amount
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(80, now)
	assert.NoError(t, err)
	assert.Equal(t, " |50.00|50/100", afterTrack(row))
}

func TestThemedFileProgressHint(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	const amount, rate = "5.0 / 10.0 MiB", " | 1.0 MiB/s"
	for _, tc := range []struct {
		width int
		want  string
	}{
		{80, amount + rate + " | 5s left | 1s elapsed"},
		{40, amount + rate + " | 5s left"},
		{30, amount + rate},
		{20, amount},
		{13, ""},
	} {
		rows, err := themedBar(t, asciiTheme(false), halfFile(now)).rows(tc.width, 0, now)
		assert.NoError(t, err)
		// the bar keeps its geometry, whatever the hint shows
		assert.Equal(t, "build  50%  ", rows[0][:12].String())
		hint := ""
		if len(rows) > 1 {
			hint = rows[1].String()
		}
		assert.Equal(t, tc.want, hint)
	}
	for _, tc := range []struct {
		state func(*progressState)
		want  string
	}{
		// amounts in different units keep their own
		{func(s *progressState) { s.currentNum, s.sinceRedrawNum = 900<<10, 900<<10 }, "900.0 KiB / 10.0 MiB | 1.0 MiB/s"},
		{func(s *progressState) { s.maxNum, s.currentNum, s.sinceRedrawNum = 1000, 512, 512 }, "512 / 1000 B | 1.0 MiB/s"},
		// without a total, the bytes read alone
		{func(s *progressState) { s.maxNum = 0 }, "5.0 MiB | 1.0 MiB/s"},
		// WithFormatRate owns the unit of the rate
		{func(s *progressState) { s.fmtRate = func(r float64) string { return fmt.Sprintf("%.0f bytes", r) } },
			"5.0 / 10.0 MiB | 1048576 bytes /s"},
	} {
		state := halfFile(now)
		state.showEstimate, state.showElapsed = false, false
		tc.state(&state)
		rows, err := themedBar(t, asciiTheme(false), state).rows(80, 0, now)
		assert.NoError(t, err)
		assert.Equal(t, tc.want, rows[1].String())
	}
}

// afterTrack is what a row shows after the empty track of its bar.
func afterTrack(row text) string {
	s := row.String()
	return s[strings.LastIndex(s, "-")+len("-"):]
}

// scriptedFile returns its reads in order, then EOF, without touching the
// buffer, and counts the calls.
type scriptedFile struct {
	size  int64
	reads []fileRead
	calls int
}

type fileRead struct {
	n   int
	err error
}

func (r *scriptedFile) Read([]byte) (int, error) {
	r.calls++
	if len(r.reads) == 0 {
		return 0, io.EOF
	}
	c := r.reads[0]
	r.reads = r.reads[1:]
	return c.n, c.err
}

func (r *scriptedFile) Size() int64 { return r.size }

// themedFile is a file reader over src rendering through th on a terminal
// 60 columns wide.
func themedFile(t *testing.T, th Theme, src io.Reader) (*wrapReader, fakeTerminal) {
	t.Helper()
	useTheme(t, th)
	ft := newFakeTerminal(t, 60)
	r, err := NewFileProgressReader(t.Context(), src, "build", ft.options()...)
	assert.NoError(t, err)
	ft.closeOnCleanup(t, r.p)
	return r, ft
}

func TestThemedFileProgressFinalEOF(t *testing.T) {
	src := &scriptedFile{size: 4 << 20, reads: []fileRead{{1 << 20, nil}, {3 << 20, io.EOF}}}
	r, ft := themedFile(t, asciiTheme(false), src)
	buf := make([]byte, 4<<20)
	n, err := r.Read(buf)
	assert.Equal(t, 1<<20, n)
	assert.NoError(t, err)
	ft.tick(time.Second)
	assert.Equal(t, frameOf("build  25%  "+strings.Repeat("=", 11)+">"+strings.Repeat("-", 35),
		"1.0 / 4.0 MiB | 1.0 MiB/s | 3s left"), <-ft.frames)
	// the last bytes come with EOF, exactly as read
	n, err = r.Read(buf)
	assert.Equal(t, 3<<20, n)
	assert.ErrorIs(t, err, io.EOF)
	// EOF ends the bar: its rows are erased, with no completion line
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, r.p, ft.frames))
	assert.True(t, ft.restored.Load())
	assert.NoError(t, r.Close())
	// no read to find out that the file is complete
	assert.Equal(t, 2, src.calls)
}

func TestThemedFileProgressEarlyEOF(t *testing.T) {
	src := &scriptedFile{size: 100, reads: []fileRead{{30, io.EOF}}}
	r, ft := themedFile(t, asciiTheme(false), src)
	n, err := r.Read(make([]byte, 100))
	assert.Equal(t, 30, n)
	assert.ErrorIs(t, err, io.EOF)
	// a short file ends the bar without a frame claiming it complete
	assert.Equal(t, "", untilProgressStopped(t, r.p, ft.frames))
	assert.True(t, ft.restored.Load())
	assert.NoError(t, r.Close())
	assert.Equal(t, 1, src.calls)
}

func TestThemedFileProgressReadError(t *testing.T) {
	broken := errors.New("broken")
	src := &scriptedFile{size: 100, reads: []fileRead{{30, nil}, {10, broken}}}
	r, ft := themedFile(t, asciiTheme(false), src)
	buf := make([]byte, 100)
	_, err := r.Read(buf)
	assert.NoError(t, err)
	n, err := r.Read(buf)
	// the partial bytes and the error, as read
	assert.Equal(t, 10, n)
	assert.ErrorIs(t, err, broken)
	ft.tick(time.Second)
	// the partial bytes count; the error is the caller's to report
	assert.Equal(t, frameOf("build  40%  "+strings.Repeat("=", 18)+">"+strings.Repeat("-", 28),
		"40 / 100 B | 40 B/s | 1s left"), <-ft.frames)
	closed := make(chan error)
	go func() { closed <- r.Close() }()
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, r.p, ft.frames))
	assert.NoError(t, <-closed)
	assert.True(t, ft.restored.Load())
	assert.Equal(t, 2, src.calls)
}

func TestThemedFileProgressZeroSize(t *testing.T) {
	src := &scriptedFile{size: 0}
	r, ft := themedFile(t, asciiTheme(false), src)
	ft.tick(time.Second)
	// an empty file has no completion to show
	assert.Equal(t, frameOf("build  ?    "+strings.Repeat("-", 47), "0 B | 0 B/s"), <-ft.frames)
	n, err := r.Read(make([]byte, 8))
	assert.Equal(t, 0, n)
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, r.p, ft.frames))
	assert.NoError(t, r.Close())
}

func TestThemedFileProgressRenderError(t *testing.T) {
	th := NewTheme()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Progress.Hint = `{{ boom }}`
	src := &scriptedFile{size: 100, reads: []fileRead{{30, nil}, {20, nil}}}
	r, ft := themedFile(t, th, src)
	buf := make([]byte, 100)
	_, err := r.Read(buf)
	assert.NoError(t, err)
	ft.tick(time.Second)
	assert.Equal(t, "", untilProgressStopped(t, r.p, ft.frames))
	assert.True(t, ft.restored.Load())
	// reading goes on: the bar no longer receives bytes, and nothing blocks
	n, err := r.Read(buf)
	assert.Equal(t, 20, n)
	assert.NoError(t, err)
	err = r.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "progress: redraw: theme.Templates.Progress.Hint")
}

func TestThemedFileProgressLogsWithoutTerminal(t *testing.T) {
	th := NewTheme()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Progress.Label = `{{ boom }}`
	th.Templates.Progress.Metrics = `{{ boom }}`
	th.Templates.Progress.Frame = `{{ boom }}{{ .Bar }}`
	useTheme(t, th)
	var logs, out bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	r, err := NewFileProgressReader(t.Context(), &progressSizedReader{
		Reader: strings.NewReader(strings.Repeat("x", 100)), size: 100,
	}, "Download", WithOutput(&out))
	assert.NoError(t, err)
	data, err := io.ReadAll(iotest.OneByteReader(r))
	assert.NoError(t, err)
	assert.Equal(t, 100, len(data))
	assert.NoError(t, r.Close())
	// the same milestones as without a theme, and nothing on the output
	assert.Equal(t, "", out.String())
	var percentages []int
	decoder := json.NewDecoder(&logs)
	for decoder.More() {
		var entry struct {
			Percent int `json:"percent"`
		}
		assert.NoError(t, decoder.Decode(&entry))
		percentages = append(percentages, entry.Percent)
	}
	assert.Equal(t, []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}, percentages)
}
