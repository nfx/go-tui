// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"text/template"
	"time"

	"log/slog"
)

// These two styles are taken from cli-spinners (MIT License)
// See https://github.com/sindresorhus/cli-spinners/blob/main/spinners.json for more spinner styles.
var DefaultSpinnerStyle = []string{"⠉⠉", "⠈⠙", "⠀⠹", "⠀⢸", "⠀⣰", "⢀⣠", "⣀⣀", "⣄⡀", "⣆⠀", "⡇⠀", "⠏⠀", "⠋⠁"}
var SpinnerStyleDocs = []string{".  ", ".. ", "...", " ..", "  .", "   "}

// spinnerFrames and spinnerInterval are the theme preset, immune to edits of
// the exported styles above.
var spinnerFrames = []string{".  ", ".. ", "...", " ..", "  .", "   "}

const spinnerInterval = 100 * time.Millisecond

// SpinnerStyle is the animation of spinners. [WithFrames] replaces the frames
// of one spinner; the interval belongs to the whole [Spinners] group.
type SpinnerStyle struct {
	// Frames are nonempty, equally wide single-line strings. Empty takes the
	// preset.
	Frames []string
	// Interval is the animation step. Zero takes 100ms.
	Interval time.Duration
}

// normalize fills the preset and validates the frames and interval.
func (s SpinnerStyle) normalize() (SpinnerStyle, error) {
	switch {
	case s.Interval < 0:
		return s, fmt.Errorf("theme.Spinner.Interval: %v: must not be negative", s.Interval)
	case s.Interval == 0:
		s.Interval = spinnerInterval
	}
	if len(s.Frames) == 0 {
		s.Frames = slices.Clone(spinnerFrames)
	}
	if err := new(Spinners).checkFrames(s.Frames); err != nil {
		return s, fmt.Errorf("theme.Spinner.%w", err)
	}
	return s, nil
}

var longRunningNewSpinners = NewSpinners

// LongRunning shows a spinner with the provided message while cb executes.
func LongRunning(ctx context.Context, message string, cb func(context.Context) error) error {
	spinners, err := longRunningNewSpinners(ctx)
	if err != nil {
		return cb(ctx)
	}
	defer spinners.Close()
	spinner, err := spinners.Add(ctx)
	if err != nil {
		return cb(ctx)
	}
	spinner.Update(message)
	return cb(ctx)
}

type spinnerEvent interface {
	isSpinnerOutgoing()
}

type spinnerEventMarker struct{}

func (spinnerEventMarker) isSpinnerOutgoing() {}

type spinnerGroupInit struct {
	spinnerEventMarker
}

type spinnerAdded struct {
	spinnerEventMarker
	Name  string
	Index int
}

type spinnerUpdated struct {
	spinnerEventMarker
	Index   int
	Message string
}

type spinnerFailed struct {
	spinnerEventMarker
	Index int
	Error string
}

type spinnerRemoved struct {
	spinnerEventMarker
	Index int
}

type spinnerGroupClosed struct {
	spinnerEventMarker
}

type Spinners struct {
	config
	// cancel ends the group; a render failure is its cause.
	cancel  context.CancelCauseFunc
	io      *termIO
	stopped chan struct{}

	creates chan createSpinner
	updates chan updateOffset
	stops   chan int
	wg      sync.WaitGroup
	ticker  *time.Ticker
	ticks   <-chan time.Time

	state      []*spinnerState
	makeTermIO func(io.Reader, io.Writer) (*termIO, error)
	eventSink  func(spinnerEvent)

	// theme is captured with the group, so a spinner added later keeps it.
	theme  *compiledTheme
	themed bool
	// rows are the theme slots bound to the destination of the group.
	rows spinnerSlots
}

// newSpinners initializes spinner updates without taking ownership of terminal mode.
func newSpinners(ctx context.Context) *Spinners {
	ctx, cancel := context.WithCancelCause(ctx)
	theme, themed := defaultIO.currentTheme(), themeRendering.Load()
	interval := spinnerInterval
	if themed {
		interval = theme.source.Spinner.Interval
	}
	ticker := time.NewTicker(interval)
	in, out := defaultStreams(ctx)
	return &Spinners{
		ctx:        ctx,
		in:         in,
		out:        out,
		ticker:     ticker,
		ticks:      ticker.C,
		cancel:     cancel,
		theme:      theme,
		themed:     themed,
		stopped:    make(chan struct{}),
		makeTermIO: makeOutputIO,
		creates:    make(chan createSpinner),
		updates:    make(chan updateOffset),
		stops:      make(chan int),
	}
}

func NewSpinners(ctx context.Context, opt ...opt) (*Spinners, error) {
	s := newSpinners(ctx)
	started := false
	defer func() {
		if !started {
			s.cancel(nil)
			s.ticker.Stop()
		}
	}()
	err := opts(opt).Apply(s)
	if err != nil {
		return nil, err
	}
	if s.themed {
		// output only: no lease, so no probe
		look := defaultIO.bindTheme(ctx, s.theme, s.out, nil)
		s.rows, err = s.theme.templates.Spinner.bind(look.funcs)
		if err != nil {
			return nil, err
		}
	}
	s.io, err = s.makeTermIO(s.in, s.out)
	if err != nil {
		if !errors.Is(err, ErrNoTTY) {
			return nil, err
		}
		s.io = nil
		s.ticker.Stop()
		s.ticks = nil // stage changes need no animation ticks
	} else if err = s.io.restoreMode(); err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	s.emit(spinnerGroupInit{})
	started = true
	go s.start(s.ctx) //nolint:contextcheck // s.ctx derives from ctx and is canceled by Close
	return s, nil
}

func (s *Spinners) Close() {
	if s == nil {
		return // NewSpinners failed
	}
	s.cancel(nil)
	if s.stopped != nil {
		<-s.stopped
	}
}

func (s *Spinners) MustAddBackground(opt ...opt) *Spinner {
	spinner, err := s.Add(context.Background(), opt...)
	if err != nil {
		panic(err)
	}
	return spinner
}

// WithPrefixf sets the text shown next to a spinner added with [Spinners.Add].
func WithPrefixf(prefix string, args ...any) opt {
	// TODO: decide if we expose text/template or fmt. This is a bit of a mess
	return opT(func(cs *createSpinner) error {
		cs.prefix = fmt.Sprintf(prefix, args...)
		return nil
	})
}

// WithKeep will keep the spinner displayed after it's done.
func WithKeep() opt {
	return opT(func(cs *createSpinner) error {
		cs.keep = true
		return nil
	})
}

// WithFrames replaces the animation of a spinner added with [Spinners.Add].
func WithFrames(frames []string) opt {
	return opT(func(cs *createSpinner) error {
		cs.frames = slices.Clone(frames)
		return nil
	})
}

// Add creates a spinner with nonempty, equally wide single-line frames.
func (s *Spinners) Add(ctx context.Context, opt ...opt) (*Spinner, error) {
	// rewrap the context, so that we can cancel the spinner when
	// we don't want to cancel the parent context.
	ctx, cancel := context.WithCancel(ctx)
	replyOffset := make(chan int, 1)
	frames := SpinnerStyleDocs
	if s.themed {
		frames = s.theme.source.Spinner.Frames
	}
	req := createSpinner{
		cancel:      cancel,
		frames:      frames,
		replyOffset: replyOffset,
	}
	err := opts(opt).Apply(&req)
	if err == nil {
		err = s.checkFrames(req.frames)
		if err != nil {
			err = fmt.Errorf("spinner: %w", err)
		}
	}
	if err != nil {
		cancel()
		return nil, err
	}
	// when parent context is done, we can't create a spinner
	select {
	case <-s.ctx.Done():
		cancel()
		return nil, s.err()
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	case s.creates <- req:
		select {
		case <-s.ctx.Done(): // spinner group is done
			cancel()
			return nil, s.err()
		case <-ctx.Done(): // what created this spinner is done
			cancel()
			return nil, ctx.Err()
		case offset := <-replyOffset:
			// go close in background
			spinner := &Spinner{
				parent: s,
				offset: offset,
			}
			go spinner.monitor(ctx)
			return spinner, nil
		}
	}
}

// err is why the group ended: a render failure, or the cancellation.
func (s *Spinners) err() error { return context.Cause(s.ctx) }

func (s *Spinners) emit(ev spinnerEvent) {
	if s.eventSink == nil {
		return
	}
	s.eventSink(ev)
}

// start is the group loop. A failed redraw ends the group with the error as
// the cause, which [Spinners.Add] and [Spinner.Close] then return.
func (s *Spinners) start(ctx context.Context) {
	defer close(s.stopped)
	var onResize <-chan struct{}
	if s.io != nil {
		onResize = s.io.onResize
	}
	var prevActive int
	for {
		var err error
		select {
		case <-ctx.Done():
			s.shutdown(prevActive)
			return
		case ns := <-s.creates:
			// TODO: write serially in CI mode, as well as when number of spinners
			// is greater than the height of the terminal
			s.newSpinner(ns)
			s.wg.Add(1)
		case update := <-s.updates:
			s.updateSpinner(update)
		case offset := <-s.stops:
			s.stopSpinner(offset)
		case <-s.ticks:
			s.drainQueues()
			prevActive, err = s.redraw(prevActive)
		case <-onResize:
			s.drainQueues()
			prevActive, err = s.redraw(prevActive)
		}
		if err != nil {
			s.cancel(fmt.Errorf("spinners: %w", err))
			s.shutdown(prevActive)
			return
		}
	}
}

// shutdown cancels every spinner and releases the terminal, clearing the
// rows the last frame drew.
func (s *Spinners) shutdown(drawn int) {
	for offset, spinner := range s.state {
		if spinner == nil {
			continue
		}
		slog.Debug("cancelling spinner")
		spinner.cancel()
		s.markDone(offset)
	}
	s.stop(drawn)
}

// drainQueues ensures we apply pending spinner mutations before rendering a frame.
// This helps tick events run after any concurrent updates/stops/creates that were
// queued in the same moment, making tests deterministic.
func (s *Spinners) drainQueues() {
	for {
		select {
		case ns := <-s.creates:
			s.newSpinner(ns)
			s.wg.Add(1)
		case update := <-s.updates:
			s.updateSpinner(update)
		case offset := <-s.stops:
			s.stopSpinner(offset)
		default:
			return
		}
	}
}

// updateOffset is a concurrent client for [Spinners.updateSpinner].
func (s *Spinners) updateOffset(offset int, message string, err error) {
	select {
	case <-s.ctx.Done():
		return
	default:
	}
	select {
	case <-s.ctx.Done():
		return
	case s.updates <- updateOffset{
		offset:  offset,
		message: message,
		err:     err,
	}: // ok
		return
	}
}

type updateOffset struct {
	offset  int
	message string
	err     error
}

// updateSpinner is a serial handler for [Spinners.updateOffset].
func (s *Spinners) updateSpinner(update updateOffset) {
	if s.state[update.offset] == nil {
		return // it's already stopped and we don't care
	}
	if s.state[update.offset].Done || s.state[update.offset].Failed {
		return // a kept or failed spinner keeps showing how it ended
	}
	if update.err != nil {
		update.message = update.err.Error()
		s.state[update.offset].Failed = true
		s.state[update.offset].Message = text(update.message)
		s.logStage(slog.LevelError, update.message, update.offset, "failed")
		s.emit(spinnerFailed{
			Index: update.offset,
			Error: update.message,
		})
		return
	}
	if s.state[update.offset].Message.String() != update.message {
		s.logStage(slog.LevelInfo, update.message, update.offset, "running")
	}
	s.state[update.offset].Message = text(update.message)
	s.emit(spinnerUpdated{
		Index:   update.offset,
		Message: update.message,
	})
}

// concurrent client for [Spinners.stopSpinner].
func (s *Spinners) stopOffset(offset int) error {
	// select picks randomly among ready cases, so check ctx first to never
	// enqueue once stopped.
	select {
	case <-s.ctx.Done():
		return s.err()
	default:
	}
	select {
	case <-s.ctx.Done():
		return s.err()
	case s.stops <- offset:
		return nil
	}
}

// stopSpinner is a serial handler for [Spinners.stopOffset].
func (s *Spinners) stopSpinner(offset int) {
	if offset >= 0 && offset < len(s.state) {
		if s.state[offset] == nil {
			return // very unlikely, but just in case
		}
		s.markDone(offset)
		if s.state[offset].Failed {
			return // user needs to see the error message
		}
		if s.state[offset].keep {
			return // don't remove the spinner if it's supposed to be kept
		}
		// remove spinner at offset
		s.state[offset] = nil
		s.emit(spinnerRemoved{Index: offset})
	}
}

func (s *Spinners) markDone(offset int) {
	if offset < 0 || offset >= len(s.state) {
		return
	}
	spinner := s.state[offset]
	if spinner == nil || spinner.Done {
		return
	}
	spinner.Done = true
	if !spinner.Failed {
		s.logStage(slog.LevelInfo, "spinner stopped", offset, "stopped")
	}
	if spinner.cancel != nil {
		spinner.cancel() // release the child context and its monitor
	}
	s.wg.Done()
}

// redraw renders all displayed spinners as single terminal rows, replacing
// the prevActive rows of the previous frame. It returns the rows drawn, and
// writes nothing when a row fails to render.
func (s *Spinners) redraw(prevActive int) (int, error) {
	s.io.refreshSize()
	frame := bytes.NewBuffer(make([]byte, 2*s.io.Width))
	frame.Reset()
	if prevActive > 0 {
		if err := s.io.clear(prevActive, frame); err != nil {
			return prevActive, err
		}
	}
	currActive := 0
	var line text
	for _, spinner := range s.state {
		if spinner == nil {
			continue
		}
		if s.themed {
			row, err := s.themedRow(spinner)
			if err != nil {
				return prevActive, err
			}
			line = row
		} else {
			line = s.legacyRow(line[:0], spinner)
		}
		frame.WriteByte('\r')
		frame.Write(line)
		frame.WriteByte('\n')
		frame.WriteByte('\r')
		currActive++
	}
	if _, err := frame.WriteTo(s.io); err != nil {
		return currActive, fmt.Errorf("write: %w", err)
	}
	return currActive, nil
}

// legacyRow appends the animated row of a spinner to line, truncated to the
// terminal width.
func (s *Spinners) legacyRow(line text, spinner *spinnerState) text {
	spinner.next()
	line = append(line, spinner.frames[spinner.tick]...)
	line = append(line, ' ')
	if len(spinner.Prefix) > 0 {
		line = append(append(line, spinner.Prefix...), ": "...)
	}
	line = append(line, spinner.Message...)
	if s.io.Width > 0 && line.width() > s.io.Width {
		return line.truncateVisible(s.io.Width, ' ')
	}
	return line
}

// themedRow renders a spinner through the slot of its state. Only a running
// spinner advances its frame. The row is clipped before the last column, so
// that it cannot autowrap.
func (s *Spinners) themedRow(spinner *spinnerState) (text, error) {
	slot, state := s.rows.running, "running"
	switch {
	case spinner.Failed:
		slot, state = s.rows.failed, "failed"
	case spinner.Done:
		slot, state = s.rows.stopped, "stopped"
	default:
		spinner.next()
	}
	row, err := slot.render(spinnerView{
		Frame:   spinner.frames[spinner.tick],
		Prefix:  s.oneLine(spinner.Prefix),
		Message: s.oneLine(spinner.Message),
		State:   state,
	})
	if err != nil {
		return nil, err
	}
	if cols := s.theme.geometry(s.io.Width, 0).columns(nil); row.width() > cols {
		row = row.truncateColumns(cols)
	}
	return row, nil
}

// oneLine keeps the text and SGR of caller content on one row: line breaks
// and tabs become spaces, invalid bytes U+FFFD, and other controls and
// escape sequences are dropped.
func (*Spinners) oneLine(t text) string {
	var out text
	for seg := range t.segments() {
		_, sgr := seg.sgrParams()
		switch {
		case seg.isText(), seg.isEscape && sgr:
			out = append(out, seg.text...)
		case seg.isInvalid:
			out = append(out, "\uFFFD"...)
		case seg.isControl && bytes.ContainsAny(seg.text, "\r\n\t"):
			out = append(out, ' ')
		}
	}
	return out.String()
}

// stop clears the drawn rows of the last frame and releases the terminal.
func (s *Spinners) stop(drawn int) {
	s.emit(spinnerGroupClosed{})
	s.clearOnStop(drawn)
	s.restoreOnStop()
	s.ticker.Stop()
	// channels are not closed: senders exit via s.ctx.Done()
}

// restoreOnStop keeps failed and kept spinners in the scrollback and gives the viewport back.
func (s *Spinners) restoreOnStop() {
	if s.io == nil {
		return
	}
	if err := s.io.Restore(); err != nil {
		slog.Debug("restore spinners", "err", err)
	}
}

func (s *Spinners) clearOnStop(drawn int) {
	if s.io == nil || drawn == 0 {
		return
	}
	_, ok := s.io.out.(*chanIO)
	if ok && s.io.vp == nil {
		return
	}
	err := s.io.clear(drawn, s.io)
	if err != nil {
		slog.Debug("clear spinners", "err", err)
	}
}

type createSpinner struct {
	cancel      context.CancelFunc
	frames      []string
	replyOffset chan int
	prefix      string
	keep        bool
}

func (s *Spinners) newSpinner(ns createSpinner) {
	offset := len(s.state)
	s.state = append(s.state, &spinnerState{
		cancel: ns.cancel,
		Prefix: text(ns.prefix),
		tick:   (len(s.state) + 1) % len(ns.frames),
		frames: ns.frames,
		active: true,
		keep:   ns.keep,
	})
	s.logStage(slog.LevelInfo, "spinner started", offset, "started")
	s.emit(spinnerAdded{
		Name:  ns.prefix,
		Index: offset,
	})
	select {
	case <-s.ctx.Done():
		return
	case ns.replyOffset <- offset:
	}
}

// logStage runs on the group loop, preserving update order without logging animation frames.
func (s *Spinners) logStage(level slog.Level, message string, offset int, status string) {
	if s.io != nil {
		return
	}
	slog.Default().Log(s.ctx, level, message,
		"spinner", offset,
		"prefix", s.state[offset].Prefix.String(),
		"status", status,
	)
}

type spinnerState struct {
	cancel  context.CancelFunc
	tick    int
	active  bool
	keep    bool
	frames  []string
	Prefix  text
	Message text
	Failed  bool
	Done    bool
}

func (ss *spinnerState) next() {
	ss.tick = (ss.tick + 1) % len(ss.frames)
}

// spinnerView is the data of the spinner slots. Prefix and Message are
// caller text on one line; State is running, stopped or failed, as in the
// non-terminal logs.
type spinnerView struct {
	Frame   string
	Prefix  string
	Message string
	State   string
}

// spinnerSlots are the spinner slots bound to the destination of a group.
type spinnerSlots struct{ running, stopped, failed boundSlot }

// bind attaches destination helpers to every spinner slot.
func (t spinnerTemplates) bind(funcs template.FuncMap) (s spinnerSlots, _ error) {
	var errs [3]error
	s.running, errs[0] = t.Running.bind(funcs)
	s.stopped, errs[1] = t.Stopped.bind(funcs)
	s.failed, errs[2] = t.Failed.bind(funcs)
	return s, errors.Join(errs[:]...)
}

type Spinner struct {
	parent *Spinners
	offset int
}

// Close will stop the spinner and remove it from display if it's not kept.
func (s *Spinner) Close() error { // TODO: what about s.cancel?..
	return s.parent.stopOffset(s.offset)
}

// Update will update the spinner with a new message.
func (s *Spinner) Update(message string) {
	s.parent.updateOffset(s.offset, message, nil)
}

// Updatef will update the spinner with a formatted message.
func (s *Spinner) Updatef(format string, args ...any) {
	s.parent.updateOffset(s.offset, fmt.Sprintf(format, args...), nil)
}

// Fail will stop the spinner and display an error message.
func (s *Spinner) Fail(err error) {
	s.parent.updateOffset(s.offset, "", err)
}

//nolint:errcheck // TODO: handle error
func (s *Spinner) monitor(ctx context.Context) {
	defer s.Close() // we send the stop to the parent with the offset
	select {        // whether parent or self context is done
	case <-s.parent.ctx.Done():
		return // all spinners are done
	case <-ctx.Done():
		return // what created spinner is done
	}
}

// checkFrames requires nonempty, single-line text/SGR frames of equal positive display width.
func (s *Spinners) checkFrames(frames []string) error {
	if len(frames) == 0 {
		return errors.New("frames must not be empty")
	}
	width := 0
	for i, f := range frames {
		if seg, ok := text(f).unstyled(); ok {
			return fmt.Errorf("frames[%d]: %q: %q is not text or SGR", i, f, seg.text)
		}
		w := text(f).width()
		switch {
		case w == 0:
			return fmt.Errorf("frames[%d]: %q: want a positive display width", i, f)
		case i > 0 && w != width:
			return fmt.Errorf("frames[%d]: %q: width %d, want %d as frames[0]", i, f, w, width)
		}
		width = w
	}
	return nil
}
