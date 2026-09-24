// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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

func WithFormatRate(f func(float64) string) opt {
	return progressbarOpt(func(p *Progressbar) error {
		p.fmtRate = f

		return nil
	})
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
	progressState
	label      string
	increments chan int64
	cancel     context.CancelFunc
	io         *termIO
	makeTermIO func(io.Reader, io.Writer) (*termIO, error)
	ticker     *time.Ticker
	ticks      <-chan time.Time
	now        func() time.Time
	err        error
}

// NewMaxProgressBar returns progress bar towards the max number.
func NewMaxProgressBar(label string, size int64, opts ...opt) (*Progressbar, error) {
	var err error
	p := newProgressbar()
	for _, o := range opts {
		err = o(p)
		if err != nil {
			return nil, fmt.Errorf("apply option: %w", err)
		}
	}
	p.showRate = true
	p.showEstimate = true
	p.maxNum = size
	p.io, err = p.makeTermIO(p.in, p.out)
	if err != nil {
		return nil, fmt.Errorf("make io: %w", err)
	}
	err = p.io.Restore()
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	p.label = label
	p.startedAt = p.now()
	go p.start(p.ctx)

	return p, nil
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
	defer p.stop() //nolint:errcheck // best effort
	frame := bytes.NewBuffer(make([]byte, 2*p.io.Width))
	frame.Reset()
	labelWidth := width([]byte(p.label)) + 1
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
			done := p.tick(frame, labelWidth)
			if done {
				return
			}
		}
	}
}

func (p *Progressbar) tick(frame *bytes.Buffer, labelWidth int) bool {
	err := p.io.clear(1, frame)
	if err != nil {
		p.err = fmt.Errorf("clear: %w", err)
	}
	frame.WriteByte('\r')
	frame.WriteString(p.label)
	frame.WriteString(" ")
	err = p.render(frame, p.io.Width-labelWidth, p.now())
	if err != nil {
		p.err = fmt.Errorf("redraw: %w", err)

		return true
	}
	frame.WriteByte('\n')
	frame.WriteByte('\r')
	_, err = frame.WriteTo(p.io)
	if err != nil {
		p.err = fmt.Errorf("redraw: %w", err)

		return true
	}

	return p.isDone()
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

type progressState struct {
	maxNum         int64
	currentNum     int64
	sinceRedrawNum int64
	redrawAt       time.Time
	startedAt      time.Time
	rollingRates   []float64
	elapsed        time.Duration
	fmtRate        func(float64) string
	showEstimate   bool
	showRate       bool
	showElapsed    bool
}

func (p *progressState) isDone() bool {
	if p.maxNum <= 0 {
		return false
	}

	return p.currentNum >= p.maxNum
}

func (p *progressState) render(frame *bytes.Buffer, width int, now time.Time) error {
	p.increment(now)
	rollingRate := p.rollingRate()
	completion := 0.0
	if p.maxNum > 0 {
		completion = float64(p.currentNum) / float64(p.maxNum)
	}
	tmp := frame.Len()
	_, err := fmt.Fprintf(frame, "%d%% ", int(completion*100))
	if err != nil {
		return err
	}
	rightPad := frame.Len() - tmp
	right := []string{}
	if p.showRate {
		if p.fmtRate == nil {
			p.fmtRate = func(f float64) string { return fmt.Sprintf("%.2f", f) }
		}
		part := p.fmtRate(rollingRate) + "/s"
		right = append(right, part)
		rightPad += len(part) + 2 // `, `
	}
	if p.showEstimate {
		part := p.remainingTime(rollingRate)
		if part != "" {
			right = append(right, part)
			rightPad += len(part) + 2 // `, `
		}
	}
	if p.showElapsed {
		part := fmt.Sprintf("%s elapsed", p.elapsed.Truncate(time.Second))
		right = append(right, part)
		rightPad += len(part) + 2 // `, `
	}
	bar := p.filledBarLine(width-rightPad-3, completion)
	_, err = fmt.Fprint(frame, bar)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(frame, " (%s)", strings.Join(right, ", "))
	if err != nil {
		return err
	}

	return nil
}

func (p *progressState) increment(now time.Time) {
	increment := p.currentNum - p.sinceRedrawNum
	if increment == 0 {
		return
	}
	p.sinceRedrawNum = p.currentNum
	p.elapsed = p.redrawAt.Sub(p.startedAt)
	since := now.Sub(p.redrawAt)
	completionRate := float64(increment) / since.Seconds()
	p.rollingRates = append(p.rollingRates, completionRate)
	if len(p.rollingRates) > 5 {
		p.rollingRates = p.rollingRates[1:] // keep only the last 10 rates
	}
	p.redrawAt = now
}

func (p *progressState) remainingTime(rollingRate float64) string {
	remainingNum := p.maxNum - p.currentNum
	remainingTime := time.Duration(float64(remainingNum)/rollingRate*1) * time.Second
	if rollingRate > 0 {
		return fmt.Sprintf("%s remaining", remainingTime)
	}

	return ""
}

func (p *progressState) filledBarLine(width int, completion float64) string {
	filledWidth := int(float64(width) * completion)
	if filledWidth > width {
		filledWidth = width
	}
	bar := "["
	for i := range width {
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

func (p *progressState) rollingRate() float64 {
	if len(p.rollingRates) == 0 {
		return 0.0
	}
	var sum float64
	for _, rate := range p.rollingRates {
		sum += rate
	}

	return sum / float64(len(p.rollingRates))
}

type fileStat interface {
	Stat() (os.FileInfo, error)
}

type sized interface {
	Size() int64
}

var errNoSize = errors.New("unable to determine size of reader")

type wrapReader struct {
	r io.Reader
	p *Progressbar
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
	p.showRate = true
	p.showEstimate = true
	p.maxNum = size
	p.io, err = p.makeTermIO(p.in, p.out)
	if err != nil {
		return nil, fmt.Errorf("make io: %w", err)
	}
	err = p.io.Restore()
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	p.label = label
	p.startedAt = p.now()
	go p.start(p.ctx)

	return wrap, nil
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
	if err == io.EOF {
		w.p.cancel()
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
