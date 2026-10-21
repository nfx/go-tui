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
	"log/slog"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
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
	Remaining int64   // seconds; -1 when no rate is available yet
	Elapsed   int64   // seconds
}

type progressClosed struct {
	progressEventMarker
	Label string
}

func WithFormatRate(f func(float64) string) opt {
	return opT(func(p *Progressbar) error {
		p.fmtRate = f
		return nil
	})
}

// WithWorkers configures the amount of parallel workers for NewParallelProgressBar.
func WithWorkers(workers int) opt {
	return opT(func(p *Progressbar) error {
		if workers <= 0 {
			return fmt.Errorf("%w: workers must be greater than 0", ErrInvalidState)
		}
		p.workers = workers
		return nil
	})
}

var runtimeNumCPU = runtime.NumCPU

// ProgressStyle is the bar width of progress bars. Its glyphs are
// [Symbols.BarFill], [Symbols.BarHead] and [Symbols.BarEmpty].
type ProgressStyle struct {
	// MinBarWidth is the narrowest bar kept by dropping metrics, the last
	// first. Zero takes 1.
	MinBarWidth int
	// MaxBarWidth bounds the bar. Zero leaves it the rest of the row.
	MaxBarWidth int
}

// progressStyle is the theme preset.
var progressStyle = ProgressStyle{MinBarWidth: 1}

// normalize fills the preset and validates the widths.
func (s ProgressStyle) normalize() (ProgressStyle, error) {
	def := progressStyle
	switch {
	case s.MinBarWidth < 0:
		return s, fmt.Errorf("theme.Progress.MinBarWidth: %d: must not be negative", s.MinBarWidth)
	case s.MaxBarWidth < 0:
		return s, fmt.Errorf("theme.Progress.MaxBarWidth: %d: must not be negative", s.MaxBarWidth)
	case s.MinBarWidth == 0:
		s.MinBarWidth = def.MinBarWidth
	}
	if s.MaxBarWidth > 0 && s.MaxBarWidth < s.MinBarWidth {
		return s, fmt.Errorf("theme.Progress.MaxBarWidth: %d: below MinBarWidth %d", s.MaxBarWidth, s.MinBarWidth)
	}
	return s, nil
}

// newProgressbar initializes progress updates without taking ownership of terminal mode.
func newProgressbar(ctx context.Context) *Progressbar {
	ctx, cancel := context.WithCancelCause(ctx)
	ticker := time.NewTicker(100 * time.Millisecond)
	in, out := defaultStreams(ctx)
	return &Progressbar{
		ctx:        ctx,
		in:         in,
		out:        out,
		theme:      defaultIO.currentTheme(),
		ticker:     ticker,
		ticks:      ticker.C,
		cancel:     cancel,
		makeTermIO: makeOutputIO,
		increments: make(chan int64),
		stopped:    make(chan struct{}),
		now:        time.Now,
		workers:    runtimeNumCPU(),
	}
}

// newStartedProgressBar starts a bar, rendering through the captured theme.
func newStartedProgressBar(ctx context.Context, label string, size int64, o ...opt) (*Progressbar, error) {
	var err error
	p := newProgressbar(ctx)
	err = opts(o).Apply(p)
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
	p.io, err = p.makeTermIO(p.in, p.out)
	if errors.Is(err, ErrNoTTY) {
		return p, nil // continue like nothing happens
	} else if err != nil {
		p.cancel(err)
		return nil, fmt.Errorf("make io: %w", err)
	}
	err = p.io.restoreMode()
	if err != nil {
		p.cancel(err)
		return nil, fmt.Errorf("restore: %w", err)
	}
	p.label = text(label)
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
	config
	progressState
	label          text
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

	// theme is captured with the bar; look binds it to the destination.
	theme *compiledTheme
	look  *progressLook
}

// NewMaxProgressBar returns progress bar towards the max number.
func NewMaxProgressBar(ctx context.Context, label string, size int64, opts ...opt) (*Progressbar, error) {
	return newStartedProgressBar(ctx, label, size, opts...)
}

// NewSliceProgressBar returns progress bar that updates as long as iterator consumed.
func NewSliceProgressBar[T any](ctx context.Context, label string, slice []T, opts ...opt) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		p, err := newStartedProgressBar(ctx, label, int64(len(slice)), opts...)
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
func NewParallelProgressBar[T any](
	ctx context.Context,
	label string,
	slice []T,
	yield func(T) error,
	opts ...opt,
) error {
	// one bar, theme and binding for all workers; they only add to it
	p, err := newStartedProgressBar(ctx, label, int64(len(slice)), opts...)
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
	closeErr := p.Close()
	if runner.firstErr == nil && closeErr != nil {
		// workers were only cancelled because rendering failed
		runErr = nil
	}
	err = errors.Join(runErr, closeErr)
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
	case <-p.stopped: // renderer exited, nobody receives increments anymore
		return
	case p.increments <- num:
	}
}

func (p *Progressbar) Close() error {
	if p.io == nil {
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
		case <-p.io.onResize:
			if p.tick(frame) {
				return
			}
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
	line := make(text, 0, max(p.io.Width, 4))
	line = append(line, '\r')
	// reserve one column to avoid the terminal's autowrap column
	usable := p.io.Width - 1
	contentWidth := 0
	row, err := p.row(usable, now)
	if err != nil {
		p.err = fmt.Errorf("redraw: %w", err)
		return true
	}
	line = append(line, row...)
	if usable > 0 {
		contentWidth = line[1:].width()
	}
	line = append(line, '\n', '\r')
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

func (p *Progressbar) flushLine(frame *bytes.Buffer, line text) error {
	if _, err := frame.Write(line); err != nil {
		return err
	}
	_, err := frame.WriteTo(p.io)
	return err
}

func (p *Progressbar) stop() error {
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
	// bytes counts the bytes of a file, shown in binary units
	bytes bool
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

// formatRate formats a rate with [WithFormatRate], without its unit.
func (p *progressState) formatRate(rate float64) string {
	if p.fmtRate == nil {
		return fmt.Sprintf("%.2f", rate)
	}
	return p.fmtRate(rate)
}

func (p *progressState) increment(now time.Time) {
	p.elapsed = now.Sub(p.startedAt)
	increment := p.currentNum - p.sinceRedrawNum
	if increment == 0 {
		return
	}
	p.sinceRedrawNum = p.currentNum
	since := now.Sub(p.redrawAt)
	completionRate := float64(increment) / since.Seconds()
	p.rollingRates = append(p.rollingRates, completionRate)
	keep := min(10, int(p.currentNum/10)) // keep last max 10% of rates
	if len(p.rollingRates) > keep {
		p.rollingRates = p.rollingRates[1:] // keep only the last 10 rates
	}
	p.redrawAt = now
}

func (p *progressState) remainingSeconds(rollingRate float64) time.Duration {
	remainingNum := p.maxNum - p.currentNum
	if remainingNum <= 0 {
		return 0
	}
	remainingTime := time.Duration(float64(remainingNum)/rollingRate*1) * time.Second
	return remainingTime
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
	remaining := int64(-1) // estimate unavailable until a rate is known
	if rate > 0 {
		remaining = int64(p.remainingSeconds(rate).Seconds())
	}
	return progressUpdate{
		Complete:  p.completion(),
		Rate:      rate,
		Remaining: remaining,
		Elapsed:   int64(p.elapsed.Seconds()),
	}
}

// progressView is the data of every progress slot, from one snapshot of the
// state. Rate, Remaining and Elapsed are empty when dropped to fit the row;
// Remaining also while unknown, which is distinct from "0s".
type progressView struct {
	// Label is the caller label on one line; in Frame, the Label slot.
	Label string
	// Metrics is the Metrics slot, in Frame only.
	Metrics string
	// Bar is the bar, sized to the row, in Frame only.
	Bar     string
	Current int64
	Total   int64
	// Percent is the completion, 0 to 100; 0 without a positive Total.
	Percent int
	// Amount is the bytes read of a file and, with a positive Total, its
	// size, in binary units: "3.0 MiB / 10.0 MiB". Empty for counted bars.
	Amount string
	// Rate is formatted by [WithFormatRate], without the "/s" unit; for a
	// file, in binary units without it.
	Rate      string
	Remaining string
	Elapsed   string
	// Values are the shown metrics with their units, in the order they are
	// kept: amount, rate, remaining and elapsed time.
	Values []string
}

// view returns the progress data with the first n of the metrics shown.
// rateText is the rate as formatted for display.
func (p *Progressbar) view(rate float64, rateText string, n int) progressView {
	v := progressView{
		Label:   p.label.oneLine(),
		Current: p.currentNum,
		Total:   p.maxNum,
		Percent: min(100, max(0, int(p.completion()*100))),
	}
	add := func(field *string, value, shown string) {
		if len(v.Values) < n {
			*field = value
			v.Values = append(v.Values, shown)
		}
	}
	if p.bytes {
		amount := byteSize(float64(p.currentNum))
		if p.maxNum > 0 {
			amount += " / " + byteSize(float64(p.maxNum))
		}
		add(&v.Amount, amount, amount)
	}
	if p.showRate {
		add(&v.Rate, rateText, rateText+"/s")
	}
	if p.showEstimate && rate > 0 && p.maxNum > 0 {
		d := p.remainingSeconds(rate).String()
		add(&v.Remaining, d, d+" remaining")
	}
	if p.showElapsed {
		d := p.elapsed.Truncate(time.Second).String()
		add(&v.Elapsed, d, d+" elapsed")
	}
	return v
}

// row renders the row of the bar in width columns. It drops
// metrics, the last first, until the bar keeps MinBarWidth; the bar then
// takes the columns the rest of the frame leaves, up to MaxBarWidth. A row
// that does not fit even without metrics is clipped.
func (p *Progressbar) row(width int, now time.Time) (text, error) {
	p.increment(now)
	if width <= 0 {
		return nil, nil
	}
	rate := p.rollingRate()
	rateText := ""
	if p.showRate && p.bytes && p.fmtRate == nil {
		rateText = byteSize(rate)
	} else if p.showRate {
		rateText = text(p.formatRate(rate)).oneLine()
	}
	style := p.look.style
	for n := len(p.view(rate, rateText, math.MaxInt).Values); n >= 0; n-- {
		before, after, err := p.look.around(p.view(rate, rateText, n))
		if err != nil {
			return nil, err
		}
		bar := width - before.width() - after.width()
		if style.MaxBarWidth > 0 {
			bar = min(bar, style.MaxBarWidth)
		}
		if bar < style.MinBarWidth && n > 0 {
			continue
		}
		row := slices.Concat(before, p.look.bar(max(bar, 0), p.completion()), after)
		if row.width() > width {
			row = row.truncateColumns(width)
		}
		return row, nil
	}
	return nil, nil
}

// byteUnits are the binary units above bytes.
var byteUnits = [...]string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

// byteSize formats n bytes in binary units: whole bytes below 1 KiB, then
// one decimal, as "512 B" or "1.5 MiB".
func byteSize(n float64) string {
	if math.Round(n) < 1024 {
		return fmt.Sprintf("%.0f B", n)
	}
	n /= 1024
	unit := 0
	// move up once the value would print as 1024.0
	for unit < len(byteUnits)-1 && math.Round(n*10) >= 1024*10 {
		n /= 1024
		unit++
	}
	return fmt.Sprintf("%.1f %s", n, byteUnits[unit])
}

// progressBarMark stands in for the bar while the frame renders. Slots
// cannot emit it, since it is a control character.
const progressBarMark = "\x00"

// progressLook is the theme of one bar, bound to its destination.
type progressLook struct {
	binding               binding
	style                 ProgressStyle
	symbols               Symbols
	label, metrics, frame boundSlot
}

// bindLook binds the captured theme to the output of the bar. Output
// only: without a lease, it never probes the terminal.
func (p *Progressbar) bindLook(ctx context.Context) (err error) {
	p.look, err = p.bind(defaultIO.bindTheme(ctx, p.theme, p.out, nil))
	return err
}

// bind attaches the destination helpers of look to every progress slot.
func (p *Progressbar) bind(look binding) (*progressLook, error) {
	t := p.theme.templates.Progress
	l := &progressLook{binding: look, style: p.theme.source.Progress, symbols: p.theme.source.Symbols}
	var errs [3]error
	l.label, errs[0] = t.Label.bind(look.funcs)
	l.metrics, errs[1] = t.Metrics.bind(look.funcs)
	l.frame, errs[2] = t.Frame.bind(look.funcs)
	return l, errors.Join(errs[:]...)
}

// around renders the slots of v and returns the frame before and after the
// bar.
func (l *progressLook) around(v progressView) (before, after text, err error) {
	label, err := l.label.render(v)
	if err != nil {
		return nil, nil, err
	}
	metrics, err := l.metrics.render(v)
	if err != nil {
		return nil, nil, err
	}
	v.Label, v.Metrics, v.Bar = label.String(), metrics.String(), progressBarMark
	out, err := l.frame.execute(v)
	if err != nil {
		return nil, nil, err
	}
	before, after, ok := bytes.Cut(out, []byte(progressBarMark))
	if !ok || bytes.Contains(after, []byte(progressBarMark)) {
		return nil, nil, fmt.Errorf("theme.Templates.%s: want the bar once", l.frame.tmpl.Name())
	}
	for _, part := range [][]byte{before, after} {
		if err := l.frame.styled(part); err != nil {
			return nil, nil, err
		}
	}
	return before, after, nil
}

// bar draws width cells at completion: the done cells and the head, which
// marks a partly done cell, in the accent role, then the track in the guide
// role.
func (l *progressLook) bar(width int, completion float64) text {
	// tolerate rounding, so that 5% of 20 cells is one cell without a head
	const epsilon = 1e-9
	exact := float64(width) * completion
	done := min(width, max(0, int(exact+epsilon)))
	cells := strings.Repeat(l.symbols.BarFill, done)
	if exact-float64(done) > epsilon && done < width {
		cells += l.symbols.BarHead
		done++
	}
	return slices.Concat(
		l.binding.style(l.binding.roles.accent, text(cells)),
		l.binding.style(l.binding.roles.guide, text(strings.Repeat(l.symbols.BarEmpty, width-done))),
	)
}

type fileStat interface {
	Stat() (os.FileInfo, error)
}

type sized interface {
	Size() int64
}

var errNoSize = errors.New("unable to determine size of reader")

type wrapReader struct {
	r             io.Reader
	p             *Progressbar
	progressMu    sync.Mutex
	read          int64
	loggedPercent int
}

func NewFileProgressReader(ctx context.Context, r io.Reader, label string, o ...opt) (*wrapReader, error) {
	p := newProgressbar(ctx)
	started := false
	defer func() {
		if !started {
			p.cancel(nil)
			p.ticker.Stop()
		}
	}()
	err := opts(o).Apply(p)
	if err != nil {
		p.cancel(err)
		return nil, fmt.Errorf("apply option: %w", err)
	}
	wrap := &wrapReader{r: r, p: p}
	size, err := wrap.Size()
	if err != nil {
		return nil, fmt.Errorf("size: %w", err)
	}
	err = p.bindLook(ctx)
	if err != nil {
		return nil, err
	}
	p.bytes = true
	p.showRate = true
	p.showEstimate = true
	p.maxNum = size
	p.label = text(label)
	p.io, err = p.makeTermIO(p.in, p.out)
	if errors.Is(err, ErrNoTTY) {
		p.io = nil
		p.ticker.Stop()
		started = true
		return wrap, nil
	}
	if err != nil {
		return nil, fmt.Errorf("make io: %w", err)
	}
	err = p.io.restoreMode()
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	p.startedAt = p.now()
	p.redrawAt = p.startedAt
	p.emit(progressInit{
		Label: p.label.String(),
		Max:   p.maxNum,
	})
	started = true
	go p.start(p.ctx) //nolint:contextcheck // p.ctx derives from ctx and is canceled by Close
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
	if w.p.io == nil {
		w.logProgress(n, errors.Is(err, io.EOF))
	}
	if n > 0 {
		w.p.Add(int64(n))
	}
	if errors.Is(err, io.EOF) {
		w.p.cancel(nil)
	}
	return n, err
}

// logProgress reports the latest decile reached by a read, without replaying skipped milestones.
func (w *wrapReader) logProgress(n int, eof bool) {
	w.progressMu.Lock()
	defer w.progressMu.Unlock()
	w.read += int64(n)
	percent := 0
	if w.p.maxNum > 0 {
		percent = int(min(100, float64(w.read)/float64(w.p.maxNum)*100)) / 10 * 10
	} else if w.p.maxNum == 0 && w.read == 0 && eof {
		percent = 100
	}
	if percent <= w.loggedPercent {
		return
	}
	w.loggedPercent = percent
	slog.Default().InfoContext(w.p.ctx, "file progress",
		"label", w.p.label.String(),
		"percent", percent,
		"read", w.read,
		"size", w.p.maxNum,
	)
}

func (w *wrapReader) Close() error {
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
