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
	"time"

	"log/slog"
)

// These two styles are taken from cli-spinners (MIT License)
// See https://github.com/sindresorhus/cli-spinners/blob/main/spinners.json for more spinner styles.
var DefaultSpinnerStyle = []string{"⠉⠉", "⠈⠙", "⠀⠹", "⠀⢸", "⠀⣰", "⢀⣠", "⣀⣀", "⣄⡀", "⣆⠀", "⡇⠀", "⠏⠀", "⠋⠁"}
var SpinnerStyleDocs = []string{".  ", ".. ", "...", " ..", "  .", "   "}

var longRunningNewSpinners = NewSpinners

// LongRunning shows a spinner with the provided message while cb executes.
func LongRunning(ctx context.Context, message string, cb func(context.Context) error) error {
	spinners, err := longRunningNewSpinners(WithContext(ctx))
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
	cancel  context.CancelFunc
	io      *termIO
	stopped chan struct{}

	creates chan createSpinner
	updates chan updateOffset
	stops   chan int
	wg      sync.WaitGroup
	ticker  *time.Ticker
	ticks   <-chan time.Time

	state      []*spinnerState
	displayed  int
	makeTermIO func(io.Reader, io.Writer) (*termIO, error)
	eventSink  func(spinnerEvent)
}

func newSpinners() *Spinners {
	ctx, cancel := context.WithCancel(context.Background())
	ticker := time.NewTicker(100 * time.Millisecond)
	in, out := defaultStreams()
	return &Spinners{
		ctx:        ctx,
		in:         in,
		out:        out,
		ticker:     ticker,
		ticks:      ticker.C,
		cancel:     cancel,
		stopped:    make(chan struct{}),
		makeTermIO: makeTermIO,
		creates:    make(chan createSpinner),
		updates:    make(chan updateOffset),
		stops:      make(chan int),
	}
}

func NewSpinners(opt ...opt) (*Spinners, error) {
	s := newSpinners()
	err := opts(opt).Apply(s)
	if err != nil {
		return nil, err
	}
	s.io, err = s.makeTermIO(s.in, s.out)
	if err != nil {
		return nil, err
	}
	err = s.io.restoreMode() // spinners only draw, so they never need raw mode
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	s.emit(spinnerGroupInit{})
	go s.start(s.ctx)
	return s, nil
}

func (s *Spinners) Close() {
	s.cancel()
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

func (s *Spinners) Add(ctx context.Context, opt ...opt) (*Spinner, error) {
	// rewrap the context, so that we can cancel the spinner when
	// we don't want to cancel the parent context.
	ctx, cancel := context.WithCancel(ctx)
	replyOffset := make(chan int, 1)
	req := createSpinner{
		cancel:      cancel,
		frames:      SpinnerStyleDocs,
		replyOffset: replyOffset,
	}
	err := opts(opt).Apply(&req)
	if err == nil && len(req.frames) == 0 {
		err = errors.New("spinner: frames must not be empty")
	}
	if err != nil {
		cancel()
		return nil, err
	}
	// when parent context is done, we can't create a spinner
	select {
	case <-s.ctx.Done():
		cancel()
		return nil, s.ctx.Err()
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	case s.creates <- req:
		select {
		case <-s.ctx.Done(): // spinner group is done
			cancel()
			return nil, s.ctx.Err()
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

func (s *Spinners) setContext(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)
}

func (s *Spinners) emit(ev spinnerEvent) {
	if s.eventSink == nil {
		return
	}
	s.eventSink(ev)
}

func (s *Spinners) start(ctx context.Context) {
	defer close(s.stopped)
	// defer s.stop()
	var prevActive int
	for {
		select {
		case <-ctx.Done():
			for offset, spinner := range s.state {
				if spinner == nil {
					continue
				}
				slog.Debug("cancelling spinner")
				spinner.cancel()
				s.markDone(offset)
			}
			s.stop()
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
			prevActive = s.redraw(prevActive)
		case <-s.io.onResize:
			s.drainQueues()
			prevActive = s.redraw(prevActive)
		}
	}
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
		s.emit(spinnerFailed{
			Index: update.offset,
			Error: update.message,
		})
		return
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
		return s.ctx.Err()
	default:
	}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
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
		s.displayed--
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
	if spinner.cancel != nil {
		spinner.cancel() // release the child context and its monitor
	}
	s.wg.Done()
}

// redraw renders all active spinners as single terminal rows.
//
//nolint:errcheck // TODO: add error handling in Spinners state
func (s *Spinners) redraw(prevActive int) int {
	s.io.refreshSize()
	frame := bytes.NewBuffer(make([]byte, 2*s.io.Width))
	frame.Reset()
	if prevActive > 0 {
		s.io.clear(prevActive, frame)
	}
	currActive := 0
	var line text
	for _, spinner := range s.state {
		if spinner == nil {
			continue
		}
		spinner.next()
		frame.WriteByte('\r')
		line = append(line[:0], spinner.frames[spinner.tick]...)
		line = append(line, ' ')
		if len(spinner.Prefix) > 0 {
			line = append(append(line, spinner.Prefix...), ": "...)
		}
		line = append(line, spinner.Message...)
		// truncate to terminal width so each spinner stays on one row
		if s.io.Width > 0 && line.width() > s.io.Width {
			frame.Write(line.truncateVisible(s.io.Width, ' '))
		} else {
			frame.Write(line)
		}
		frame.WriteByte('\n')
		frame.WriteByte('\r')
		currActive++
	}
	frame.WriteTo(s.io)
	return currActive
}

func (s *Spinners) stop() {
	s.emit(spinnerGroupClosed{})
	s.clearOnStop()
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

func (s *Spinners) clearOnStop() {
	if s.io == nil {
		return
	}
	_, ok := s.io.out.(*chanIO)
	if ok && s.io.vp == nil {
		return
	}
	err := s.io.clear(s.displayed, s.io)
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
	s.displayed++
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
