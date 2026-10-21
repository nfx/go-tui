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

// ProgressStyle is the geometry of progress bars. Their glyphs are
// [Symbols.BarFill], [Symbols.BarHead] and [Symbols.BarEmpty].
type ProgressStyle struct {
	// MinBarWidth is the narrowest bar kept by dropping metrics, the last
	// first, or by shortening the label. Zero takes 1.
	MinBarWidth int
	// MaxBarWidth bounds the bar. Zero leaves it the rest of the row.
	MaxBarWidth int
	// Compact keeps the label, the percentage, the bar and the metrics on
	// one row, dropping metrics to keep the bar. Otherwise the label, the
	// percentage and the bar share a row and the metrics go to a hint row
	// below it; a terminal without room for both rows gets the compact row.
	Compact bool
}

// progressStyle is the theme preset.
var progressStyle = ProgressStyle{MinBarWidth: 1}

// progressPercent is the completion of a bar left-aligned in four columns,
// or the Unknown symbol without a total.
const progressPercent = `{{ if gt .Total 0 }}{{ accent (printf "%-4s" (printf "%d%%" .Percent)) }}` +
	`{{ else }}{{ accent (printf "%-4s" (symbol "Unknown")) }}{{ end }}`

// stackedProgressFrame is the built-in frame of bars that are not
// [ProgressStyle.Compact], which sets the label apart from the percentage.
const stackedProgressFrame = `{{ with .Label }}{{ . }}  {{ end }}` + progressPercent +
	` {{ .Bar }}{{ with .Metrics }} {{ . }}{{ end }}`

// builtins returns the built-in templates with the frame of the layout.
func (s ProgressStyle) builtins() Templates {
	t := builtinTemplates
	if !s.Compact {
		t.Progress.Frame = stackedProgressFrame
	}
	return t
}

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
	label      text
	increments chan int64
	stopped    chan struct{}
	cancel     context.CancelCauseFunc
	io         *termIO
	makeTermIO func(io.Reader, io.Writer) (*termIO, error)
	ticker     *time.Ticker
	ticks      <-chan time.Time
	now        func() time.Time
	err        error
	rendered   bool
	// rowWidths are the columns of every row of the last written frame.
	rowWidths []int
	eventSink func(progressEvent)
	workers   int

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

// tick refreshes geometry, composes the rows of the bar from one snapshot
// and writes them, replacing the previous frame, at once.
func (p *Progressbar) tick(frame *bytes.Buffer) bool {
	p.io.refreshSize()
	// reserve one column to avoid the terminal's autowrap column
	height := p.io.Height
	if p.io.vp == nil && height > 1 {
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
		if err := p.io.clear(p.linesToClear(p.io.Width), frame); err != nil {
			p.err = fmt.Errorf("clear: %w", err)
		}
	}
	widths := make([]int, len(rows))
	out := make(text, 0, 2*max(p.io.Width, 4)*len(rows))
	for i, row := range rows {
		out = append(out, '\r')
		out = append(out, row...)
		out = append(out, '\n')
		widths[i] = row.width()
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

func (p *Progressbar) flushLine(frame *bytes.Buffer, line text) error {
	if _, err := frame.Write(line); err != nil {
		return err
	}
	_, err := frame.WriteTo(p.io)
	return err
}

func (p *Progressbar) stop() error {
	if p.rendered {
		p.io.refreshSize()
		err := p.io.clear(p.linesToClear(p.io.Width), p.io)
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

// progressView is the data of every progress slot but Hint, from one
// snapshot of the state. Rate, Remaining and Elapsed are empty when dropped
// to fit the row; Remaining also while unknown, which is distinct from "0s".
type progressView struct {
	// Label is the caller label on one line; in Frame, the Label slot.
	Label string
	// Metrics is the Metrics slot, in the Frame of [ProgressStyle.Compact]
	// bars only.
	Metrics string
	// Bar is the bar, sized to the row, in Frame only.
	Bar     string
	Current int64
	// Total is the size of the work; not positive when unknown.
	Total int64
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
	// Hints are the shown metrics as pairs of a value and its unit, in the
	// same order, led by the count of a counted bar with a total. The Hint
	// slot renders them.
	Hints hintView
}

// progressMetric is one metric of a snapshot: its value for the legacy
// field of its kind, the value with its units for Values, and its pair for
// Hints. A metric with only a pair, such as the count of a counted bar, has
// no kind.
type progressMetric struct {
	kind  progressMetricKind
	value string
	shown string
	pair  hint
}

// progressMetricKind names the legacy field of a metric.
type progressMetricKind int

const (
	pairOnly progressMetricKind = iota
	amountMetric
	rateMetric
	remainingMetric
	elapsedMetric
)

// metrics returns every available metric of the current state, in the
// order they are kept: amount, rate, remaining and elapsed time.
func (p *Progressbar) metrics(rate float64) []progressMetric {
	var ms []progressMetric
	switch {
	case p.bytes:
		amount := p.byteSize(float64(p.currentNum))
		if p.maxNum > 0 {
			amount += " / " + p.byteSize(float64(p.maxNum))
		}
		ms = append(ms, progressMetric{kind: amountMetric, value: amount, shown: amount, pair: p.amountPair()})
	case p.maxNum > 0:
		count := hint{Left: text(fmt.Sprintf("%d / %d", p.currentNum, p.maxNum)), Right: text("items")}
		ms = append(ms, progressMetric{pair: count})
	}
	if p.showRate {
		ms = append(ms, p.rateMetric(rate))
	}
	if p.showEstimate && rate > 0 && p.maxNum > 0 {
		d := p.remainingSeconds(rate).String()
		ms = append(ms, progressMetric{
			kind: remainingMetric, value: d, shown: d + " remaining",
			pair: hint{Left: text(d), Right: text("left")},
		})
	}
	if p.showElapsed {
		d := p.elapsed.Truncate(time.Second).String()
		ms = append(ms, progressMetric{
			kind: elapsedMetric, value: d, shown: d + " elapsed",
			pair: hint{Left: text(d), Right: text("elapsed")},
		})
	}
	return ms
}

// amountPair is the bytes read and the size of a file. Amounts in one unit
// share it; otherwise each keeps its own, and the pair has no right side.
func (p *Progressbar) amountPair() hint {
	read, unit := p.byteParts(float64(p.currentNum))
	if p.maxNum <= 0 {
		return hint{Left: text(read), Right: text(unit)}
	}
	size, sizeUnit := p.byteParts(float64(p.maxNum))
	if unit != sizeUnit {
		return hint{Left: text(read + " " + unit + " / " + size + " " + sizeUnit)}
	}
	return hint{Left: text(read + " / " + size), Right: text(unit)}
}

// rateMetric is the rate of the bar: bytes per second for a file, unless
// [WithFormatRate] formats it, which then owns the unit before "/s".
func (p *Progressbar) rateMetric(rate float64) progressMetric {
	if p.bytes && p.fmtRate == nil {
		value, unit := p.byteParts(rate)
		shown := value + " " + unit
		return progressMetric{
			kind: rateMetric, value: shown, shown: shown + "/s",
			pair: hint{Left: text(value), Right: text(unit + "/s")},
		}
	}
	value := text(p.formatRate(rate)).oneLine()
	right := "/s"
	if p.fmtRate == nil {
		right = "items/s"
	}
	return progressMetric{
		kind: rateMetric, value: value, shown: value + "/s",
		pair: hint{Left: text(value), Right: text(right)},
	}
}

// view returns the progress data with the metrics ms shown.
func (p *Progressbar) view(ms []progressMetric) progressView {
	v := progressView{
		Label:   p.label.oneLine(),
		Current: p.currentNum,
		Total:   p.maxNum,
		Percent: min(100, max(0, int(p.completion()*100))),
	}
	for _, m := range ms {
		v.Hints = append(v.Hints, m.pair)
		var field *string
		switch m.kind {
		case amountMetric:
			field = &v.Amount
		case rateMetric:
			field = &v.Rate
		case remainingMetric:
			field = &v.Remaining
		case elapsedMetric:
			field = &v.Elapsed
		default:
			continue
		}
		*field = m.value
		v.Values = append(v.Values, m.shown)
	}
	return v
}

// rows renders the bar in width columns and at most height rows from one
// snapshot of the state, in the layout of the theme. A height below two
// rows takes the compact row; a height that is not positive is unknown.
func (p *Progressbar) rows(width, height int, now time.Time) ([]text, error) {
	p.increment(now)
	if width <= 0 {
		return []text{nil}, nil
	}
	ms := p.metrics(p.rollingRate())
	if !p.look.style.Compact && (height <= 0 || height >= 2) {
		return p.stacked(width, ms)
	}
	row, err := p.compact(width, ms)
	return []text{row}, err
}

// compact renders the single row of the bar in width columns. It drops
// metrics, the last first, until the bar keeps MinBarWidth; the bar then
// takes the columns the rest of the frame leaves, up to MaxBarWidth. A row
// that does not fit even without metrics is clipped.
func (p *Progressbar) compact(width int, ms []progressMetric) (text, error) {
	for n := len(ms); n >= 0; n-- {
		v := p.view(ms[:n])
		label, err := p.look.label.render(v)
		if err != nil {
			return nil, err
		}
		metrics, err := p.look.metrics.render(v)
		if err != nil {
			return nil, err
		}
		before, after, err := p.look.around(v, label, metrics)
		if err != nil {
			return nil, err
		}
		bar := p.look.barWidth(width - before.width() - after.width())
		if bar < p.look.style.MinBarWidth && n > 0 {
			continue
		}
		return p.look.fill(width, before, bar, after, p.completion()), nil
	}
	return nil, nil
}

// stacked renders the bar row and, below it, the hint row of the metrics
// that fit, each in width columns. The hint drops whole metrics, the last
// first, and is omitted when none fits. The label is shortened, and then
// dropped, before the bar goes below MinBarWidth; a row that does not fit
// even without the label is clipped.
func (p *Progressbar) stacked(width int, ms []progressMetric) ([]text, error) {
	hint, n, err := p.look.fitHint(width, ms)
	if err != nil {
		return nil, err
	}
	v := p.view(ms[:n])
	label, err := p.look.label.render(v)
	if err != nil {
		return nil, err
	}
	for {
		before, after, err := p.look.around(v, label, nil)
		if err != nil {
			return nil, err
		}
		bar := p.look.barWidth(width - before.width() - after.width())
		if short := p.look.style.MinBarWidth - bar; short > 0 && len(label) > 0 {
			// one column of what is left goes to the ellipsis
			if keep := label.width() - short; keep >= 2 {
				label = label.truncateColumns(keep)
			} else {
				label = nil
			}
			continue
		}
		row := p.look.fill(width, before, bar, after, p.completion())
		if hint.width() == 0 {
			return []text{row}, nil
		}
		return []text{row, hint}, nil
	}
}

// byteUnits are the binary units above bytes.
var byteUnits = [...]string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

// byteSize formats n bytes in binary units: whole bytes below 1 KiB, then
// one decimal, as "512 B" or "1.5 MiB".
func (p *Progressbar) byteSize(n float64) string {
	value, unit := p.byteParts(n)
	return value + " " + unit
}

// byteParts is [Progressbar.byteSize] apart from its unit.
func (p *Progressbar) byteParts(n float64) (value, unit string) {
	if math.Round(n) < 1024 {
		return fmt.Sprintf("%.0f", n), "B"
	}
	n /= 1024
	i := 0
	// move up once the value would print as 1024.0
	for i < len(byteUnits)-1 && math.Round(n*10) >= 1024*10 {
		n /= 1024
		i++
	}
	return fmt.Sprintf("%.1f", n), byteUnits[i]
}

// progressBarMark stands in for the bar while the frame renders. Slots
// cannot emit it, since it is a control character.
const progressBarMark = "\x00"

// progressLook is the theme of one bar, bound to its destination.
type progressLook struct {
	binding                     binding
	style                       ProgressStyle
	symbols                     Symbols
	label, metrics, hint, frame boundSlot
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
	var errs [4]error
	l.label, errs[0] = t.Label.bind(look.funcs)
	l.metrics, errs[1] = t.Metrics.bind(look.funcs)
	l.hint, errs[2] = t.Hint.bind(look.funcs)
	l.frame, errs[3] = t.Frame.bind(look.funcs)
	return l, errors.Join(errs[:]...)
}

// fitHint renders the hint of the first metrics of ms that fit in width
// columns, the last dropped first, and returns how many it shows. Without
// a fitting metric, the hint is empty and shows none; a hidden hint shows
// all of them.
func (l *progressLook) fitHint(width int, ms []progressMetric) (text, int, error) {
	if l.hint.tmpl == nil {
		return nil, len(ms), nil
	}
	pairs := make(hintView, len(ms))
	for i, m := range ms {
		pairs[i] = m.pair
	}
	for n := len(pairs); n > 0; n-- {
		out, err := l.hint.render(pairs[:n])
		if err != nil {
			return nil, 0, err
		}
		if out.width() <= width {
			return out, n, nil
		}
	}
	return nil, 0, nil
}

// barWidth bounds the columns left for the bar by MaxBarWidth.
func (l *progressLook) barWidth(cols int) int {
	if l.style.MaxBarWidth > 0 {
		return min(cols, l.style.MaxBarWidth)
	}
	return cols
}

// fill joins the frame around a bar of width cells at completion, clipped
// to cols columns.
func (l *progressLook) fill(cols int, before text, width int, after text, completion float64) text {
	row := slices.Concat(before, l.bar(max(width, 0), completion), after)
	if row.width() > cols {
		row = row.truncateColumns(cols)
	}
	return row
}

// around renders the frame of v with the rendered label and metrics, and
// returns it before and after the bar.
func (l *progressLook) around(v progressView, label, metrics text) (before, after text, err error) {
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
