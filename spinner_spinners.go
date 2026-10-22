// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/render"
	"io"
	"sync"
	"time"

	"log/slog"
)

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
	theme *render.Compiled
	// rows are the theme slots bound to the destination of the group.
	rows spinnerSlots
}

// newSpinners initializes spinner updates without taking ownership of terminal mode.
func newSpinners(ctx context.Context) *Spinners {
	ctx, cancel := context.WithCancelCause(ctx)
	theme := defaultIO.currentTheme()
	ticker := time.NewTicker(theme.Source.Spinner.Interval)
	in, out := defaultStreams(ctx)
	return &Spinners{
		ctx:        ctx,
		in:         in,
		out:        out,
		ticker:     ticker,
		ticks:      ticker.C,
		cancel:     cancel,
		theme:      theme,
		stopped:    make(chan struct{}),
		makeTermIO: makeOutputIO,
		creates:    make(chan createSpinner),
		updates:    make(chan updateOffset),
		stops:      make(chan int),
	}
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

// Add creates a spinner that animates the frames of the theme of the group.
func (s *Spinners) Add(ctx context.Context, opt ...opt) (*Spinner, error) {
	// rewrap the context, so that we can cancel the spinner when
	// we don't want to cancel the parent context.
	ctx, cancel := context.WithCancel(ctx)
	replyOffset := make(chan int, 1)
	req := createSpinner{
		cancel:      cancel,
		frames:      s.theme.Source.Symbols.Spinner,
		replyOffset: replyOffset,
	}
	err := opts(opt).Apply(&req)
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
			// TODO: write serially when number of spinners is greater than the
			// height of the terminal
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
		s.state[update.offset].Message = ansi.Text(update.message)
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
	s.state[update.offset].Message = ansi.Text(update.message)
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
	for _, spinner := range s.state {
		if spinner == nil {
			continue
		}
		line, err := s.row(spinner)
		if err != nil {
			return prevActive, err
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

// row renders a spinner through the slot of its state. Only a running
// spinner advances its frame. The row is clipped before the last column, so
// that it cannot autowrap.
func (s *Spinners) row(spinner *spinnerState) (ansi.Text, error) {
	slot, state := s.rows.running, "running"
	switch {
	case spinner.Failed:
		slot, state = s.rows.failed, "failed"
	case spinner.Done:
		slot, state = s.rows.stopped, "stopped"
	default:
		spinner.next()
	}
	row, err := slot.Render(spinnerView{
		Frame:   spinner.frames[spinner.tick],
		Prefix:  s.oneLine(spinner.Prefix),
		Message: s.oneLine(spinner.Message),
		State:   state,
	})
	if err != nil {
		return nil, err
	}
	if cols := s.theme.Geometry(s.io.Width, 0).Columns(nil); row.Width() > cols {
		row = row.TruncateColumns(cols)
	}
	return row, nil
}

// oneLine keeps the text and SGR of caller content on one row: line breaks
// and tabs become spaces, invalid bytes U+FFFD, and other controls and
// escape sequences are dropped.
func (*Spinners) oneLine(t ansi.Text) string {
	var out ansi.Text
	for seg := range t.Segments() {
		_, sgr := seg.SGRParams()
		switch {
		case seg.IsText(), seg.IsEscape && sgr:
			out = append(out, seg.Text...)
		case seg.IsInvalid:
			out = append(out, "\uFFFD"...)
		case seg.IsControl && bytes.ContainsAny(seg.Text, "\r\n\t"):
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
		Prefix: ansi.Text(ns.prefix),
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
