// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package progress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
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
func (p *Progressbar) row(width int, now time.Time) (ansi.Text, error) {
	rows, err := p.rows(width, 0, now)
	if err != nil {
		return nil, err
	}
	var out ansi.Text
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

	cio := &term.ChanIO{
		Ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := start

	terst.UseIO(t, cio, cio)
	p, err := newStartedProgressBar(t.Context(), "download", 20,
		option.OpT(func(pb *Progressbar) error {
			pb.now = func() time.Time { return now }
			pb.redrawAt = start
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     out,
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

	assert.Equal(t, 39, ansi.Text(output).Width())
}

func TestProgressbarTickRenders(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &term.ChanIO{
		Ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := start

	terst.UseIO(t, cio, cio)
	p, err := newStartedProgressBar(t.Context(), "download", 20,
		option.OpT(func(pb *Progressbar) error {
			pb.now = func() time.Time { return now }
			pb.redrawAt = start
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     out,
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
	assert.Contains(t, terst.StripSGR(output), "download  50%  ╸ 10.00/s · 1s remaining")

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
	assert.Contains(t, terst.StripSGR(second), "download  75% ")
}

func TestProgressbarEmitsStructuredEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &term.ChanIO{
		Ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := start
	events := make(chan progressEvent, 16)

	terst.UseIO(t, cio, cio)
	p, err := newStartedProgressBar(t.Context(), "download", 20,
		option.OpT(func(pb *Progressbar) error {
			pb.eventSink = func(ev progressEvent) {
				events <- ev
			}
			pb.now = func() time.Time { return now }
			pb.redrawAt = start
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     out,
					Width:   40,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)
	assert.NoError(t, err)

	initEv, ok := terst.Recv(t, events).(progressInit)
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
	metrics, ok := terst.Recv(t, events).(progressUpdate)
	assert.True(t, ok)
	assert.Equal(t, int64(1), metrics.Remaining)
	assert.Equal(t, int64(1), metrics.Elapsed)

	assert.NoError(t, p.Close())
	closed, ok := terst.Recv(t, events).(progressClosed)
	assert.True(t, ok)
	assert.Equal(t, "download", closed.Label)
}

func TestProgressbarDoneEmitsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	cio := &term.ChanIO{
		Ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := start
	events := make(chan progressEvent, 16)

	terst.UseIO(t, cio, cio)
	p, err := newStartedProgressBar(t.Context(), "sync", 2,
		option.OpT(func(pb *Progressbar) error {
			pb.eventSink = func(ev progressEvent) {
				events <- ev
			}
			pb.now = func() time.Time { return now }
			pb.redrawAt = start
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     out,
					Width:   40,
					Height:  1,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)
	assert.NoError(t, err)

	_, ok := terst.Recv(t, events).(progressInit)
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
	_, ok = terst.Recv(t, events).(progressUpdate)
	assert.True(t, ok)
	closed, ok := terst.Recv(t, events).(progressClosed)
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
	_, err := NewMaxProgressBar(t.Context(), "max", 1, option.OpT(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
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

	cio := &term.ChanIO{
		Ctx: ctx,
		In:  make(chan string),
		Out: make(chan string, 4),
	}
	ticks := make(chan time.Time, 1)
	applied := false

	terst.UseIO(t, cio, cio)
	r, err := NewFileProgressReader(t.Context(), &statReader{data: []byte("hello world")}, "file",
		option.OpT(func(pb *Progressbar) error {
			applied = true
			pb.ticks = ticks
			pb.ticker = time.NewTicker(time.Hour)
			pb.increments = make(chan int64, 8)
			pb.now = func() time.Time { return time.Now() }
			pb.redrawAt = pb.now()
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     out,
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
			terst.UseIO(t, nil, &out)
			r, err := NewFileProgressReader(t.Context(), &progressSizedReader{
				Reader: strings.NewReader(tc.data), size: tc.size,
			}, "Download")
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
			assert.ErrorIs(t, r.p.Ctx.Err(), context.Canceled)
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

func TestProgressBarLogsWithoutTTY(t *testing.T) {
	noTTY := option.OpT(func(p *Progressbar) error {
		p.makeTermIO = func(io.Reader, io.Writer) (*term.IO, error) { return nil, errs.ErrNoTTY }
		return nil
	})
	for _, tc := range []struct {
		name string
		run  func(t *testing.T)
		want []int
	}{
		{"slice", func(t *testing.T) {
			t.Helper()
			for _, err := range NewSliceProgressBar(t.Context(), "Items", make([]int, 20), noTTY) {
				assert.NoError(t, err)
			}
		}, []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}},
		{"parallel", func(t *testing.T) {
			t.Helper()
			err := NewParallelProgressBar(t.Context(), "Items", make([]int, 20), func(int) error {
				return nil
			}, WithWorkers(4), noTTY)
			assert.NoError(t, err)
		}, []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}},
		{"large steps", func(t *testing.T) {
			t.Helper()
			p, err := NewMaxProgressBar(t.Context(), "Items", 100, noTTY)
			assert.NoError(t, err)
			p.Add(55)
			p.Add(45)
			assert.NoError(t, p.Close())
		}, []int{50, 100}},
		{"ends early", func(t *testing.T) {
			t.Helper()
			p, err := NewMaxProgressBar(t.Context(), "Items", 100, noTTY)
			assert.NoError(t, err)
			p.Add(25)
			assert.NoError(t, p.Close())
			p.Add(75)
		}, []int{20}},
		{"empty", func(t *testing.T) {
			t.Helper()
			for range NewSliceProgressBar(t.Context(), "Items", []int{}, noTTY) {
			}
		}, []int{100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			tc.run(t)
			var percentages []int
			decoder := json.NewDecoder(&logs)
			for decoder.More() {
				var entry struct {
					Msg     string `json:"msg"`
					Label   string `json:"label"`
					Percent int    `json:"percent"`
					Total   int64  `json:"total"`
				}
				assert.NoError(t, decoder.Decode(&entry))
				assert.Equal(t, "progress", entry.Msg)
				assert.Equal(t, "Items", entry.Label)
				percentages = append(percentages, entry.Percent)
			}
			assert.Equal(t, tc.want, percentages)
		})
	}
}

func TestFileProgressReaderReturnsTerminalErrors(t *testing.T) {
	_, err := NewFileProgressReader(t.Context(), strings.NewReader("data"), "Download",
		option.OpT(func(p *Progressbar) error {
			p.makeTermIO = func(io.Reader, io.Writer) (*term.IO, error) {
				return nil, io.ErrClosedPipe
			}
			return nil
		}),
	)
	assert.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestWithFormatRate(t *testing.T) {
	p := newProgressbar(t.Context())
	err := WithFormatRate(func(_ float64) string { return "rate" })(p)
	assert.NoError(t, err)
	if p.fmtRate == nil {
		t.Fatalf("expected format rate function")
	}
}

func TestProgressbarCloseNoIO(t *testing.T) {
	p := &Progressbar{}
	if err := p.Close(); err != nil {
		t.Fatalf("expected nil error")
	}
}

func TestProgressbarCloseWithIO(t *testing.T) {
	p := &Progressbar{io: &term.IO{}, cancel: func(error) {}, err: io.EOF}
	err := p.Close()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestProgressbarCloseWaitsForBackgroundStop(t *testing.T) {
	restoreDelay := 50 * time.Millisecond
	p, err := newStartedProgressBar(t.Context(), "sync", 1, option.OpT(func(pb *Progressbar) error {
		pb.ticks = make(chan time.Time)
		pb.ticker = time.NewTicker(time.Hour)
		pb.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			return &term.IO{
				In:     in,
				Out:    &bytes.Buffer{},
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
		Config:     option.Config{Ctx: ctx},
		io:         &term.IO{},
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

// withLook binds the installed theme to p, as a started bar does.
func withLook(t *testing.T, p *Progressbar) *Progressbar {
	t.Helper()
	p.theme = term.Default.CurrentTheme()
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
		label: ansi.Text("test"),
		io: &term.IO{
			Out:     &bytes.Buffer{},
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
	out, ok := p.io.Out.(*bytes.Buffer)
	assert.True(t, ok)
	assert.Contains(t, terst.StripSGR(out.String()), "2s elapsed")
}

func TestProgressbarTickWriteError(t *testing.T) {
	p := withLook(t, &Progressbar{
		progressState: progressState{
			maxNum:     10,
			currentNum: 5,
			redrawAt:   time.Now(),
			startedAt:  time.Now().Add(-time.Second),
		},
		label: ansi.Text("test"),
		io: &term.IO{
			Out:     terst.FailingWriter{Err: io.EOF},
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
		io: &term.IO{
			Out:     &bytes.Buffer{},
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
		option.OpT(func(pb *Progressbar) error {
			pb.ticks = make(chan time.Time)
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     &bytes.Buffer{},
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
	case <-p.Ctx.Done():
	default:
		t.Fatal("expected p.ctx to be canceled after Close()")
	}
}

func TestNewFileProgressReaderWithContextCancelBound(t *testing.T) {
	parentCtx := t.Context()
	r := bytes.NewReader([]byte("test data"))
	w, err := NewFileProgressReader(parentCtx, r, "test",
		option.OpT(func(pb *Progressbar) error {
			pb.ticks = make(chan time.Time)
			pb.ticker = time.NewTicker(time.Hour)
			pb.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     &bytes.Buffer{},
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
	case <-w.p.Ctx.Done():
	default:
		t.Fatal("expected w.p.ctx to be canceled after Close()")
	}
}

func TestProgressbarAddAfterStoppedDoesNotBlock(t *testing.T) {
	stopped := make(chan struct{})
	close(stopped)
	p := &Progressbar{
		io:         &term.IO{},
		Ctx:        t.Context(),
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
func themedBar(t *testing.T, th theme.Theme, state progressState) *Progressbar {
	t.Helper()
	c, err := render.Compile(th)
	assert.NoError(t, err)
	p := &Progressbar{progressState: state, label: ansi.Text("build"), theme: c}
	p.look, err = p.bind(c.Bind(color.ProfilePlain, color.Background{}))
	assert.NoError(t, err)
	return p
}

// compactTheme is the ASCII theme with the single-row progress layout.
func compactTheme() theme.Theme {
	th := terst.ASCIITheme(false)
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

// fakeTerminal is a terminal for a bar, with manual ticks. tick renders at
// the given time since the start; each write is one receive from frames.
// restored reports a released terminal.
type fakeTerminal struct {
	frames   terst.FrameWriter
	tick     func(time.Duration)
	restored *atomic.Bool
	terminal option.Opt
}

// newFakeTerminal is a terminal width columns wide.
func newFakeTerminal(t *testing.T, width int) fakeTerminal {
	t.Helper()
	ft := fakeTerminal{frames: make(terst.FrameWriter), restored: &atomic.Bool{}}
	terst.UseIO(t, nil, ft.frames)
	ticks := make(chan time.Time)
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	clock := &atomic.Int64{}
	ft.terminal = option.OpT(func(pb *Progressbar) error {
		pb.now = func() time.Time { return start.Add(time.Duration(clock.Load())) }
		pb.ticks = ticks
		pb.makeTermIO = func(in io.Reader, _ io.Writer) (*term.IO, error) {
			return &term.IO{In: in, Out: ft.frames, Width: width, Height: 10, Restore: func() error {
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
func (ft fakeTerminal) options() []option.Opt {
	return []option.Opt{ft.terminal}
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
	th theme.Theme,
	size int64,
) (*Progressbar, terst.FrameWriter, func(time.Duration), *atomic.Bool) {
	t.Helper()
	terst.UseTheme(t, th)
	ft := newFakeTerminal(t, 40)
	p, err := NewMaxProgressBar(t.Context(), "build", size, ft.options()...)
	assert.NoError(t, err)
	ft.closeOnCleanup(t, p)
	return p, ft.frames, ft.tick, ft.restored
}

// untilProgressStopped returns everything the bar writes until its loop
// ends, failing the test unless it ends within a second.
func untilProgressStopped(t *testing.T, p *Progressbar, frames terst.FrameWriter) string {
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
	assert.NoError(t, new(term.IO).Clear(n, &b))
	return b.String()
}

// parallelRun is a parallel bar running in the background on a terminal 40
// columns wide; done receives its result.
type parallelRun struct {
	fakeTerminal
	done chan error
}

// runParallel runs yield over items with NewParallelProgressBar under the
// installed theme and gate.
func runParallel(ctx context.Context, t *testing.T, items []int, yield func(int) error, o ...option.Opt) parallelRun {
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

// afterTrack is what a row shows after the empty track of its bar.
func afterTrack(row ansi.Text) string {
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
func themedFile(t *testing.T, th theme.Theme, src io.Reader) (*WrapReader, fakeTerminal) {
	t.Helper()
	terst.UseTheme(t, th)
	ft := newFakeTerminal(t, 60)
	r, err := NewFileProgressReader(t.Context(), src, "build", ft.options()...)
	assert.NoError(t, err)
	ft.closeOnCleanup(t, r.p)
	return r, ft
}

func TestWithOptionsPropagatesInvalidValues(t *testing.T) {
	err := option.WithOptions(WithWorkers(0))(newProgressbar(t.Context()))
	assert.ErrorIs(t, err, errs.ErrInvalidState)
	assert.True(t, !errors.Is(err, errs.ErrWrongWidget))
}

func TestNewProgressbarUsesDefaultIO(t *testing.T) {
	prevIn := term.Default.Input()
	prevOut := term.Default.RawOutput()
	t.Cleanup(func() { term.Default.SetIO(prevIn, prevOut) })
	in := bytes.NewBufferString("in")
	out := &bytes.Buffer{}
	term.Default.SetIO(in, out)

	p := newProgressbar(t.Context())
	assert.Equal(t, in, p.In)
	assert.Equal(t, out, p.Out)
	p.ticker.Stop()
}
