// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package progress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/term"
)

// newProgressbar initializes progress updates without taking ownership of terminal mode.
func newProgressbar(ctx context.Context) *Progressbar {
	ctx, cancel := context.WithCancelCause(ctx)
	ticker := time.NewTicker(100 * time.Millisecond)
	in, out := term.DefaultStreams(ctx)
	return &Progressbar{
		ctx:        ctx,
		in:         in,
		out:        out,
		theme:      term.Default.CurrentTheme(),
		ticker:     ticker,
		ticks:      ticker.C,
		cancel:     cancel,
		makeTermIO: term.MakeOutputIO,
		increments: make(chan int64),
		stopped:    make(chan struct{}),
		now:        time.Now,
		workers:    runtimeNumCPU(),
	}
}

// newStartedProgressBar starts a bar, rendering through the captured theme.
func newStartedProgressBar(ctx context.Context, label string, size int64, o ...option.Opt) (*Progressbar, error) {
	var err error
	p := newProgressbar(ctx)
	err = option.Opts(o).Apply(p)
	if err != nil {
		p.cancel(err)
		return nil, fmt.Errorf("apply option: %w", err)
	}
	err = p.bindLook(ctx)
	if err != nil {
		p.cancel(err)
		p.ticker.Stop()
		return nil, err
	}
	p.showRate = true
	p.showEstimate = true
	p.maxNum = size
	p.label = ansi.Text(label)
	p.io, err = p.makeTermIO(p.in, p.out)
	if errors.Is(err, errs.ErrNoTTY) {
		return p, nil // logs milestones instead, see [Progressbar.logProgress]
	} else if err != nil {
		p.cancel(err)
		return nil, fmt.Errorf("make io: %w", err)
	}
	err = p.io.RestoreMode()
	if err != nil {
		p.cancel(err)
		return nil, fmt.Errorf("restore: %w", err)
	}
	p.startedAt = p.now()
	p.redrawAt = p.startedAt
	p.emit(progressInit{
		Label: p.label.String(),
		Max:   p.maxNum,
	})
	go p.start(p.ctx) //nolint:contextcheck // p.ctx derives from ctx and is canceled by Close
	return p, nil
}

type Progressbar struct {
	ctx context.Context
	in  io.Reader
	out io.Writer
	progressState
	label      ansi.Text
	increments chan int64
	stopped    chan struct{}
	cancel     context.CancelCauseFunc
	io         *term.IO
	makeTermIO func(io.Reader, io.Writer) (*term.IO, error)
	ticker     *time.Ticker
	ticks      <-chan time.Time
	now        func() time.Time
	err        error
	rendered   bool
	// rowWidths are the columns of every row of the last written frame.
	rowWidths []int
	eventSink func(progressEvent)
	workers   int

	// logMu guards the milestones logged without a terminal, which parallel
	// workers reach concurrently.
	logMu         sync.Mutex
	logged        int64
	loggedPercent int

	// theme is captured with the bar; look binds it to the destination.
	theme *render.Compiled
	look  *progressLook
}

func (p *Progressbar) Add(num int64) {
	if p.io == nil {
		if p.ctx.Err() == nil {
			p.logProgress(num, false)
		}
		return
	}
	select {
	case <-p.ctx.Done():
		return
	case <-p.stopped: // renderer exited, nobody receives increments anymore
		return
	case p.increments <- num:
	}
}

func (p *Progressbar) Close() error {
	if p.io == nil {
		if p.ctx != nil && p.ctx.Err() == nil {
			p.logProgress(0, true)
		}
		if p.cancel != nil {
			p.cancel(nil)
		}
		if p.ticker != nil {
			p.ticker.Stop()
		}
		return nil // most likely no TTY
	}
	p.cancel(nil) // p.err is owned by the renderer until it closes p.stopped
	if p.stopped != nil {
		<-p.stopped
	}
	return p.err
}

// logProgress reports the latest decile reached without a terminal, without
// replaying skipped milestones. An empty bar is complete at its end; a bar
// that ends early does not report the rest as done.
func (p *Progressbar) logProgress(n int64, end bool) {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	p.logged += n
	percent := 0
	if p.maxNum > 0 {
		percent = int(min(100, float64(p.logged)/float64(p.maxNum)*100)) / 10 * 10
	} else if p.maxNum == 0 && p.logged == 0 && end {
		percent = 100
	}
	if percent <= p.loggedPercent {
		return
	}
	p.loggedPercent = percent
	if p.bytes {
		slog.Default().InfoContext(p.ctx, "file progress",
			"label", p.label.String(),
			"percent", percent,
			"read", p.logged,
			"size", p.maxNum,
		)
		return
	}
	slog.Default().InfoContext(p.ctx, "progress",
		"label", p.label.String(),
		"percent", percent,
		"done", p.logged,
		"total", p.maxNum,
	)
}

// effectiveWorkers returns a bounded worker count for the current task.
func (p *Progressbar) effectiveWorkers(total int) int {
	if total <= 0 {
		return 0
	}
	workers := p.workers
	if workers <= 0 {
		workers = 1
	}
	if workers > total {
		return total
	}
	return workers
}

func (p *Progressbar) emit(ev progressEvent) {
	if p.eventSink == nil {
		return
	}
	p.eventSink(ev)
}

func (p *Progressbar) start(ctx context.Context) {
	defer func() {
		// p.err is published to Close by closing p.stopped
		if err := p.stop(); err != nil {
			p.err = errors.Join(p.err, err)
		}
		if p.err != nil {
			// nobody receives from p.increments anymore, so unblock Add callers
			p.cancel(p.err)
		}
		if p.stopped != nil {
			close(p.stopped)
		}
		p.emit(progressClosed{Label: p.label.String()})
	}()
	frame := bytes.NewBuffer(make([]byte, 2*p.io.Width))
	frame.Reset()
	for {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			if err != nil && !errors.Is(err, context.Canceled) {
				p.err = err
			}
			return
		case num := <-p.increments:
			p.currentNum += num
		case <-p.ticks:
			done := p.tick(frame)
			if done {
				return
			}
		case <-p.io.OnResize:
			if p.tick(frame) {
				return
			}
		}
	}
}

// tick refreshes geometry, composes the rows of the bar from one snapshot
// and writes them, replacing the previous frame, at once.
func (p *Progressbar) tick(frame *bytes.Buffer) bool {
	p.io.RefreshSize()
	// reserve one column to avoid the terminal's autowrap column
	height := p.io.Height
	if p.io.Vp == nil && height > 1 {
		// the newline after the last row moves the cursor to a row of its
		// own, which would scroll the first row off a terminal without it
		height--
	}
	rows, err := p.rows(p.io.Width-1, height, p.now())
	if err != nil {
		p.err = fmt.Errorf("redraw: %w", err)
		return true
	}
	if p.rendered {
		if err := p.io.Clear(p.linesToClear(p.io.Width), frame); err != nil {
			p.err = fmt.Errorf("clear: %w", err)
		}
	}
	widths := make([]int, len(rows))
	out := make(ansi.Text, 0, 2*max(p.io.Width, 4)*len(rows))
	for i, row := range rows {
		out = append(out, '\r')
		out = append(out, row...)
		out = append(out, '\n')
		widths[i] = row.Width()
	}
	out = append(out, '\r')
	if err := p.flushLine(frame, out); err != nil {
		p.err = fmt.Errorf("redraw: %w", err)
		return true
	}
	p.rendered = true
	p.rowWidths = widths
	p.emit(p.metricsSnapshot())
	return p.isDone()
}

// linesToClear counts the terminal rows of the last frame at width, which
// rewraps rows wider than a narrowed terminal.
func (p *Progressbar) linesToClear(width int) int {
	lines := 0
	for _, w := range p.rowWidths {
		if width <= 0 || w <= 0 {
			lines++
			continue
		}
		lines += (w + width - 1) / width
	}
	return max(lines, 1)
}

func (p *Progressbar) flushLine(frame *bytes.Buffer, line ansi.Text) error {
	if _, err := frame.Write(line); err != nil {
		return err
	}
	_, err := frame.WriteTo(p.io)
	return err
}

func (p *Progressbar) stop() error {
	if p.rendered {
		p.io.RefreshSize()
		err := p.io.Clear(p.linesToClear(p.io.Width), p.io)
		if err != nil {
			return fmt.Errorf("clear: %w", err)
		}
	}
	p.ticker.Stop()
	err := p.io.Restore()
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return nil
}
