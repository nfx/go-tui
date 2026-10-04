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
	"runtime/debug"
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
	ctx, cancel := context.WithCancelCause(context.Background())
	ticker := time.NewTicker(100 * time.Millisecond)
	return &Progressbar{
		ctx:        ctx,
		in:         defaultInput(),
		out:        defaultOutput(),
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
	label          string
	increments     chan int64
	stopped        chan struct{}
	cancel         context.CancelCauseFunc
	io             *termIO
	makeTermIO     func(io.Reader, io.Writer) (*termIO, error)
	ticker         *time.Ticker
	ticks          <-chan time.Time
	now            func() time.Time
	err            error
	rendered       bool
	lastFrameWidth int
	eventSink      func(progressEvent)
	workers        int
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
	ctx, cancel := context.WithCancelCause(p.ctx)
	runner := &parallelProgressRunner[T]{
		ctx:    ctx,
		cancel: cancel,
		p:      p,
		slice:  slice,
		yield:  yield,
		jobs:   make(chan int),
	}
	runErr := runner.run()
	err = errors.Join(runErr, p.Close())
	cancel(err)
	return err
}

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
				err:   fmt.Errorf("%w: panic: %v", ErrBug, recovered),
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
	p.cancel(p.err)
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
		case <-p.io.onResize:
			p.tick(frame)
		}
	}
}

// tick refreshes geometry and renders a single-row progress frame.
func (p *Progressbar) tick(frame *bytes.Buffer) bool {
	p.io.refreshSize()
	now := p.now()
	if p.rendered {
		lines := p.linesToClear(p.io.Width)
		if err := p.io.clear(lines, frame); err != nil {
			p.err = fmt.Errorf("clear: %w", err)
		}
	}
	line := bytes.NewBuffer(make([]byte, 0, max(p.io.Width, 4)))
	line.WriteByte('\r')
	// reserve one column to avoid the terminal's autowrap column
	usable := p.io.Width - 1
	contentWidth := 0
	labelBytes := []byte(p.label)
	labelW := width(labelBytes) + 1 // label + trailing space
	switch {
	case usable <= 0:
	case labelW > usable:
		// truncate label to fit, no room for bar/details
		line.Write(truncateVisible(labelBytes, usable, ' '))
	default:
		line.Write(labelBytes)
		line.WriteByte(' ')
		if availWidth := usable - labelW; availWidth > 0 {
			if err := p.render(line, availWidth, now); err != nil {
				p.err = fmt.Errorf("redraw: %w", err)
				return true
			}
		}
	}
	if usable > 0 {
		contentWidth = width(line.Bytes()[1:])
	}
	line.WriteByte('\n')
	line.WriteByte('\r')
	if err := p.flushLine(frame, line); err != nil {
		p.err = fmt.Errorf("redraw: %w", err)
		return true
	}
	p.rendered = true
	p.lastFrameWidth = contentWidth
	p.emit(p.metricsSnapshot())
	return p.isDone()
}

func (p *Progressbar) linesToClear(width int) int {
	if width <= 0 || p.lastFrameWidth <= 0 {
		return 1
	}
	return max((p.lastFrameWidth+width-1)/width, 1)
}

func (p *Progressbar) flushLine(frame, line *bytes.Buffer) error {
	if _, err := frame.Write(line.Bytes()); err != nil {
		return err
	}
	_, err := frame.WriteTo(p.io)
	return err
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

// completion returns 0..1, or 0 when the maximum is not positive.
func (p *progressState) completion() float64 {
	if p.maxNum <= 0 {
		return 0
	}
	return float64(p.currentNum) / float64(p.maxNum)
}

func (p *progressState) render(frame *bytes.Buffer, availWidth int, now time.Time) error {
	p.increment(now)
	rollingRate := p.rollingRate()
	completion := p.completion()
	prefix := fmt.Sprintf("%d%% ", int(completion*100))
	_, err := frame.WriteString(p.layout(rollingRate, availWidth, prefix, completion))
	return err
}

func (p *progressState) layout(rollingRate float64, availWidth int, prefix string, completion float64) string {
	if availWidth <= 0 {
		return ""
	}
	if width([]byte(prefix)) >= availWidth {
		return string(truncateVisible([]byte(prefix), availWidth, ' '))
	}
	details := p.renderDetails(rollingRate)
	for n := len(details); n >= 0; n-- {
		suffix := p.renderSuffix(details[:n], true)
		barWidth := p.barWidth(availWidth, prefix, suffix)
		if barWidth > 0 || n == 0 {
			if barWidth >= 0 {
				return prefix + p.filledBarLine(barWidth, completion) + suffix
			}
		}
		if n == 0 {
			continue
		}
		compact := p.renderSuffix(details[:n], false)
		barWidth = p.barWidth(availWidth, prefix, compact)
		if barWidth >= 0 {
			return prefix + p.filledBarLine(barWidth, completion) + compact
		}
	}
	return string(truncateVisible([]byte(prefix), availWidth, ' '))
}

func (p *progressState) renderSuffix(details []string, leadingSpace bool) string {
	if len(details) == 0 {
		return ""
	}
	if leadingSpace {
		return fmt.Sprintf(" (%s)", strings.Join(details, ", "))
	}
	return fmt.Sprintf("(%s)", strings.Join(details, ", "))
}

func (p *progressState) barWidth(availWidth int, prefix, suffix string) int {
	return availWidth - width([]byte(prefix)) - width([]byte(suffix)) - 2
}

func (p *progressState) renderDetails(rollingRate float64) []string {
	var right []string
	if p.showRate {
		right = append(right, p.renderRate(rollingRate))
	}
	if p.showEstimate {
		part := p.remainingTime(rollingRate)
		if part != "" {
			right = append(right, part)
		}
	}
	if p.showElapsed {
		right = append(right, fmt.Sprintf("%s elapsed", p.elapsed.Truncate(time.Second)))
	}
	return right
}

func (p *progressState) renderRate(rollingRate float64) string {
	if p.fmtRate == nil {
		p.fmtRate = func(f float64) string { return fmt.Sprintf("%.2f", f) }
	}
	return p.fmtRate(rollingRate) + "/s"
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
		Complete:  p.completion(),
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
		w.p.cancel(nil)
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
