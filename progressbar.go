// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

func progressbarOpt(o func(s *Progressbar) error) opt {
	return func(a any) error {
		s, ok := a.(*Progressbar)
		if !ok {
			return nil
		}
		return o(s)
	}
}

func newProgressbar() *Progressbar {
	ctx, cancel := context.WithCancel(context.Background())
	ticker := time.NewTicker(100 * time.Millisecond)
	return &Progressbar{
		config: config{
			ctx: ctx,
			in:  os.Stdin,
			out: os.Stdout,
		},
		ticker:     ticker,
		ticks:      ticker.C,
		cancel:     cancel,
		makeTermIO: makeTermIO,
		increments: make(chan int64),
		now:        time.Now,
	}
}

type Progressbar struct {
	config

	label          string
	maxNum         int64
	currentNum     int64
	sinceRedrawNum int64
	redrawAt       time.Time
	startedAt      time.Time
	rollingRates   []float64

	maxWidth   int
	increments chan int64
	cancel     context.CancelFunc
	io         *termIO
	makeTermIO func(io.Reader, io.Writer) (*termIO, error)
	ticker     *time.Ticker
	ticks      <-chan time.Time
	now        func() time.Time
	err        error
}

func (p *Progressbar) Add(num int64) {
	select {
	case <-p.ctx.Done():
		return
	case p.increments <- num:
	}
}

func (p *Progressbar) Close() error {
	p.cancel()
	return p.err
}

func (p *Progressbar) start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			err := p.stop()
			if err != nil {
				p.err = fmt.Errorf("stop: %w", err)
			}
			err = ctx.Err()
			if err != nil && err != context.Canceled {
				p.err = err
			}
			return
		case num := <-p.increments:
			p.currentNum += num
		case <-p.ticks:
			p.redraw()
		}
	}
}

func (p *Progressbar) redraw() {
	increment := p.currentNum - p.sinceRedrawNum
	p.sinceRedrawNum = p.currentNum
	p.redrawAt = p.now()
	elapsed := p.redrawAt.Sub(p.startedAt)
	completionRate := float64(increment) / elapsed.Seconds()
	p.rollingRates = append(p.rollingRates, completionRate)
	if len(p.rollingRates) > 10 {
		p.rollingRates = p.rollingRates[1:] // keep only the last 10 rates
	}
	rollingRate := p.rollingRate()
	completion := 0.0
	if p.maxNum > 0 {
		completion = float64(p.currentNum) / float64(p.maxNum)
	}
	width := 50
	bar := p.filledBarLine(width, completion)
	timeStr := p.remainingTime(rollingRate)
	progressLine := fmt.Sprintf("%s %d%% %s %s", p.label, int(completion*100), bar, timeStr)
	p.io.clear(1, p.io)
	fmt.Fprintf(p.out, "%s\r", progressLine)
}

func (p *Progressbar) remainingTime(rollingRate float64) string {
	remainingNum := p.maxNum - p.currentNum
	remainingTime := time.Duration((1/rollingRate)*(float64(remainingNum))) * time.Second
	if remainingTime.Seconds() < 0 {
		remainingTime = 0 * time.Second
	}
	timeStr := "(?)"
	if rollingRate > 0 {
		minutes := int(remainingTime.Minutes())
		if minutes > 0 {
			timeStr = fmt.Sprintf("(%dm remaining)", minutes)
		} else {
			seconds := int(remainingTime.Seconds())
			timeStr = fmt.Sprintf("(%ds remaining)", seconds)
		}
	}
	return timeStr
}

func (p *Progressbar) filledBarLine(width int, completion float64) string {
	if p.maxWidth > 0 {
		width = p.maxWidth
	}
	filledWidth := int(float64(width) * completion)
	if filledWidth > width {
		filledWidth = width
	}
	bar := "["
	for i := 0; i < width; i++ {
		if i < filledWidth {
			bar += "="
		} else if i == filledWidth {
			bar += ">"
		} else {
			bar += " "
		}
	}
	bar += "]"
	return bar
}

func (p *Progressbar) rollingRate() float64 {
	if len(p.rollingRates) == 0 {
		return 0.0
	}
	var sum float64
	for _, rate := range p.rollingRates {
		sum += rate
	}
	return sum / float64(len(p.rollingRates))
}

func (p *Progressbar) stop() error {
	err := p.io.clear(1, p.io)
	if err != nil {
		return fmt.Errorf("clear: %w", err)
	}
	p.ticker.Stop()
	err = p.io.Restore()
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return nil
}

func NewFileProgressReader(r io.Reader, label string, opts ...opt) (*wrapReader, error) {
	p := newProgressbar()
	for _, o := range opts {
		err := o(p)
		if err != nil {
			return nil, fmt.Errorf("apply option: %w", err)
		}
	}
	wrap := &wrapReader{r, p}
	size, err := wrap.Size()
	if err != nil {
		return nil, fmt.Errorf("size: %w", err)
	}
	p.out = os.Stderr
	p.label = label
	p.maxNum = size
	p.startedAt = p.now()
	p.maxWidth = 50 // default width for the progress bar
	p.io, err = p.makeTermIO(p.in, p.out)
	if err != nil {
		return nil, err
	}
	go p.start(p.ctx)
	return wrap, nil
}

type fileStat interface {
	Stat() (os.FileInfo, error)
}

type sized interface {
	Size() int64
}

var errNoSize = fmt.Errorf("unable to determine size of reader")

type wrapReader struct {
	r io.Reader
	p *Progressbar
}

func (w *wrapReader) Size() (int64, error) {
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

func (w *wrapReader) Read(p []byte) (n int, err error) {
	n, err = w.r.Read(p)
	if n > 0 {
		w.p.Add(int64(n))
	}
	return n, err
}

func (w *wrapReader) Close() error {
	err := w.p.Close()
	if err != nil {
		return fmt.Errorf("progress: %w", err)
	}
	closer, ok := w.r.(io.Closer)
	if ok {
		return closer.Close()
	}
	return nil
}
