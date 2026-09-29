// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

type progressEvent interface {
	isProgressOutgoing()
}

type progressEventMarker struct{}

func (progressEventMarker) isProgressOutgoing() {}

type progressInit struct {
	progressEventMarker
	Label string
	Max   int64
}

type progressUpdate struct {
	progressEventMarker
	Complete  float64 // 0..1
	Rate      float64 // per second
	Remaining int64   // seconds
	Elapsed   int64   // seconds
}

type progressClosed struct {
	progressEventMarker
	Label string
}

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

// WithWorkers configures the amount of parallel workers for NewParallelProgressBar.
func WithWorkers(workers int) opt {
	return progressbarOpt(func(p *Progressbar) error {
		if workers <= 0 {
			return fmt.Errorf("%w: workers must be greater than 0", ErrInvalidState)
		}
		p.workers = workers
		return nil
	})
}

var runtimeNumCPU = runtime.NumCPU

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
		stopped:    make(chan struct{}),
		now:        time.Now,
		workers:    runtimeNumCPU(),
	}
}

func newStartedProgressBar(label string, size int64, opts ...opt) (*Progressbar, error) {
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
	if errors.Is(err, ErrNoTTY) {
		return p, nil // continue like nothing happens
	} else if err != nil {
		return nil, fmt.Errorf("make io: %w", err)
	}
	err = p.io.Restore()
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	p.label = label
	p.startedAt = p.now()
	p.redrawAt = p.startedAt
	p.emit(progressInit{
		Label: p.label,
		Max:   p.maxNum,
	})
	go p.start(p.ctx)
	return p, nil
}

type Progressbar struct {
	config
	progressState
	label      string
	increments chan int64
	stopped    chan struct{}
	cancel     context.CancelFunc
	io         *termIO
	makeTermIO func(io.Reader, io.Writer) (*termIO, error)
	ticker     *time.Ticker
	ticks      <-chan time.Time
	now        func() time.Time
	err        error
	rendered   bool
	eventSink  func(progressEvent)
	workers    int
}

// NewMaxProgressBar returns progress bar towards the max number.
func NewMaxProgressBar(label string, size int64, opts ...opt) (*Progressbar, error) {
	return newStartedProgressBar(label, size, opts...)
}

// NewSliceProgressBar returns progress bar that updates as long as iterator consumed.
func NewSliceProgressBar[T any](label string, slice []T, opts ...opt) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		p, err := newStartedProgressBar(label, int64(len(slice)), opts...)
		if err != nil {
			yield(zero, err)
			return
		}
		yieldable := true
		defer func() {
			err = p.Close()
			if err != nil && yieldable {
				yield(zero, err)
			}
		}()
		for _, v := range slice {
			if !yield(v, nil) {
				yieldable = false
				return
			}
			p.Add(1)
		}
	}
}

// NewParallelProgressBar executes yield in parallel while updating progress.
func NewParallelProgressBar[T any](label string, slice []T, yield func(T) error, opts ...opt) error {
	p, err := newStartedProgressBar(label, int64(len(slice)), opts...)
	if err != nil {
		return err
	}
	if len(slice) == 0 {
		return p.Close()
	}
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	runner := &parallelProgressRunner[T]{
		ctx:    ctx,
		cancel: cancel,
		p:      p,
		slice:  slice,
		yield:  yield,
		jobs:   make(chan int),
	}
	runErr := runner.run()
	return errors.Join(runErr, p.Close())
}

type parallelProgressRunner[T any] struct {
	ctx      context.Context
	cancel   context.CancelFunc
	p        *Progressbar
	slice    []T
	yield    func(T) error
	jobs     chan int
	wg       sync.WaitGroup
	errOnce  sync.Once
	firstErr error
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
			err := r.yield(r.slice[idx])
			if err != nil {
				r.setErr(err)
				continue
			}
			r.p.Add(1)
		}
	}
}

// setErr records only the first callback error and cancels the remaining work.
func (r *parallelProgressRunner[T]) setErr(err error) {
	if err == nil {
		return
	}
	r.errOnce.Do(func() {
		r.firstErr = err
		r.cancel()
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

func (p *Progressbar) Add(num int64) {
	if p.io == nil {
		return // most likely no TTY
	}
	select {
	case <-p.ctx.Done():
		return
	case p.increments <- num:
	}
}

func (p *Progressbar) Close() error {
	if p.io == nil {
		return nil // most likely no TTY
	}
	p.cancel()
	if p.stopped != nil {
		<-p.stopped
	}
	return p.err
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
		_ = p.stop() //nolint:errcheck // best effort
		if p.stopped != nil {
			close(p.stopped)
		}
	}()
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
	if p.rendered {
		err := p.io.clear(1, frame)
		if err != nil {
			p.err = fmt.Errorf("clear: %w", err)
		}
	}
	frame.WriteByte('\r')
	frame.WriteString(p.label)
	frame.WriteString(" ")
	err := p.render(frame, p.io.Width-labelWidth, p.now())
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
	p.rendered = true
	p.emit(p.metricsSnapshot())
	return p.isDone()
}

func (p *Progressbar) stop() error {
	p.emit(progressClosed{Label: p.label})
	if p.rendered {
		err := p.io.clear(1, p.io)
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
	keep := min(10, int(p.currentNum/10)) // keep last max 10% of rates
	if len(p.rollingRates) > keep {
		p.rollingRates = p.rollingRates[1:] // keep only the last 10 rates
	}
	p.redrawAt = now
}

func (p *progressState) remainingTime(rollingRate float64) string {
	remainingTime := p.remainingSeconds(rollingRate)
	if rollingRate > 0 {
		return fmt.Sprintf("%s remaining", remainingTime)
	}
	return ""
}

func (p *progressState) remainingSeconds(rollingRate float64) time.Duration {
	remainingNum := p.maxNum - p.currentNum
	remainingTime := time.Duration(float64(remainingNum)/rollingRate*1) * time.Second
	return remainingTime
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

func (p *progressState) metricsSnapshot() progressUpdate {
	rate := p.rollingRate()
	return progressUpdate{
		Complete:  float64(p.currentNum) / float64(p.maxNum),
		Rate:      rate,
		Remaining: int64(p.remainingSeconds(rate)),
		Elapsed:   int64(p.elapsed.Seconds()),
	}
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
	p.redrawAt = p.startedAt
	p.emit(progressInit{
		Label: p.label,
		Max:   p.maxNum,
	})
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
