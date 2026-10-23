// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package spinner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/term"

	"log/slog"
)

type Spinners struct {
	ctx context.Context
	in  io.Reader
	out io.Writer
	// cancel ends the group; a render failure is its cause.
	cancel  context.CancelCauseFunc
	io      *term.IO
	stopped chan struct{}

	creates chan createSpinner
	updates chan updateOffset
	stops   chan int
	wg      sync.WaitGroup
	ticker  *time.Ticker
	ticks   <-chan time.Time

	state      []*spinnerState
	makeTermIO func(io.Reader, io.Writer) (*term.IO, error)
	eventSink  func(spinnerEvent)
	// now is the clock of the elapsed timers; tests replace it.
	now func() time.Time
	// elapsed is the default of the spinners added to the group.
	elapsed bool

	// theme is captured with the group, so a spinner added later keeps it.
	theme *render.Compiled
	// rows are the theme slots bound to the destination of the group.
	rows spinnerSlots
}

// newSpinners initializes spinner updates without taking ownership of terminal mode.
func newSpinners(ctx context.Context) *Spinners {
	ctx, cancel := context.WithCancelCause(ctx)
	theme := term.Default.CurrentTheme()
	ticker := time.NewTicker(theme.Source.Spinner.Interval)
	in, out := term.DefaultStreams(ctx)
	return &Spinners{
		ctx:        ctx,
		in:         in,
		out:        out,
		ticker:     ticker,
		ticks:      ticker.C,
		cancel:     cancel,
		theme:      theme,
		stopped:    make(chan struct{}),
		makeTermIO: term.MakeOutputIO,
		now:        time.Now,
		elapsed:    true,
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

func (s *Spinners) MustAddBackground(opt ...option.Opt) *Spinner {
	spinner, err := s.Add(context.Background(), opt...)
	if err != nil {
		panic(err)
	}
	return spinner
}

// Add creates a spinner that animates the frames of the theme of the group.
func (s *Spinners) Add(ctx context.Context, opt ...option.Opt) (*Spinner, error) {
	return s.add(ctx, -1, opt)
}

// add creates a spinner under the parent offset, or a top-level one for -1.
func (s *Spinners) add(ctx context.Context, parent int, opt []option.Opt) (*Spinner, error) {
	// rewrap the context, so that we can cancel the spinner when
	// we don't want to cancel the parent context.
	ctx, cancel := context.WithCancel(ctx)
	reply := make(chan createResult, 1)
	req := createSpinner{
		cancel:  cancel,
		frames:  s.theme.Source.Symbols.Spinner,
		reply:   reply,
		parent:  parent,
		elapsed: s.elapsed,
	}
	err := option.Opts(opt).Apply(&req)
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
			go s.release(reply)
			return nil, ctx.Err()
		case res := <-reply:
			if res.err != nil {
				cancel()
				return nil, res.err
			}
			// go close in background
			spinner := &Spinner{
				parent: s,
				offset: res.offset,
			}
			go spinner.monitor(ctx)
			return spinner, nil
		}
	}
}

// release stops a spinner that was accepted after its creator gave up, so
// that it does not run unattended.
func (s *Spinners) release(reply <-chan createResult) {
	select {
	case <-s.ctx.Done():
	case res := <-reply:
		if res.err == nil {
			s.stopOffset(res.offset) //nolint:errcheck // the group ending is the only error
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
		onResize = s.io.OnResize
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
	now := s.now()
	for offset, spinner := range s.state {
		if spinner == nil {
			continue
		}
		slog.Debug("cancelling spinner")
		spinner.cancel()
		s.markDone(offset, now)
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
	if update.offset < 0 || update.offset >= len(s.state) || s.state[update.offset] == nil {
		return // it's already stopped and we don't care
	}
	if s.state[update.offset].terminal() {
		return // a kept or failed spinner keeps showing how it ended
	}
	if update.err != nil {
		update.message = update.err.Error()
		now := s.now()
		s.state[update.offset].Failed = true
		s.state[update.offset].freeze(now)
		s.state[update.offset].Message = ansi.Text(update.message)
		s.logStage(slog.LevelError, update.message, update.offset, "failed")
		s.emit(spinnerFailed{
			Index: update.offset,
			Error: update.message,
		})
		// the failure stays with this spinner, but its children are over
		s.stopSubtree(update.offset, now, false)
		s.prune(update.offset)
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

// stopSpinner is a serial handler for [Spinners.stopOffset]. It stops the
// spinner and its running children, and removes what is neither kept nor
// failed, unless a kept or failed child still needs it as an anchor.
func (s *Spinners) stopSpinner(offset int) {
	if offset < 0 || offset >= len(s.state) || s.state[offset] == nil {
		return // very unlikely, but just in case
	}
	s.stopSubtree(offset, s.now(), true)
	s.prune(offset)
}

// subtree lists the live spinners under root, parents before children and
// siblings in creation order. It walks without recursion, as the depth is up to
// the caller.
func (s *Spinners) subtree(root int) (order []int) {
	for stack := []int{root}; len(stack) > 0; {
		offset := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		spinner := s.state[offset]
		if spinner == nil {
			continue
		}
		order = append(order, offset)
		for i := len(spinner.children) - 1; i >= 0; i-- {
			stack = append(stack, spinner.children[i])
		}
	}
	return order
}

// stopSubtree ends the running spinners under root, and root itself if asked.
func (s *Spinners) stopSubtree(root int, now time.Time, includeRoot bool) {
	for _, offset := range s.subtree(root) {
		if offset == root && !includeRoot {
			continue
		}
		s.markDone(offset, now)
	}
}

// prune removes the stopped spinners under root that are neither kept nor
// failed, children first. A spinner stays while a child remains below it.
func (s *Spinners) prune(root int) {
	order := s.subtree(root)
	for i := len(order) - 1; i >= 0; i-- {
		offset := order[i]
		spinner := s.state[offset]
		if !spinner.Done || spinner.Failed || spinner.keep {
			continue
		}
		if slices.ContainsFunc(spinner.children, func(c int) bool { return s.state[c] != nil }) {
			continue
		}
		s.state[offset] = nil
		s.emit(spinnerRemoved{Index: offset})
	}
}

func (s *Spinners) markDone(offset int, now time.Time) {
	if offset < 0 || offset >= len(s.state) {
		return
	}
	spinner := s.state[offset]
	if spinner == nil || spinner.Done {
		return
	}
	spinner.Done = true
	spinner.freeze(now)
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
	s.io.RefreshSize()
	frame := bytes.NewBuffer(make([]byte, 2*s.io.Width))
	frame.Reset()
	if prevActive > 0 {
		if err := s.io.Clear(prevActive, frame); err != nil {
			return prevActive, err
		}
	}
	now := s.now()
	cols := s.theme.Geometry(s.io.Width, 0).Columns(nil)
	shown := s.display(cols)
	hidden := 0
	// rows above the terminal would scroll away, and the next frame could not
	// clear them, so the last row stands for the ones that do not fit
	if room := s.io.Height - 1; s.io.Height > 1 && len(shown) > room {
		hidden = len(shown) - (room - 1)
		shown = shown[:room-1]
	}
	var lines []ansi.Text
	for _, r := range shown {
		line, err := s.row(s.state[r.offset], r.tree, cols, now)
		if err != nil {
			return prevActive, err
		}
		lines = append(lines, line)
	}
	if hidden > 0 {
		more := fmt.Sprintf("%s %d more", s.theme.Source.Symbols.Ellipsis, hidden)
		line, err := s.rows.tree.Render(more)
		if err != nil {
			return prevActive, err
		}
		lines = append(lines, line.TruncateColumns(cols))
	}
	s.drawRows(frame, lines)
	currActive := len(lines)
	if _, err := frame.WriteTo(s.io); err != nil {
		return currActive, fmt.Errorf("write: %w", err)
	}
	if currActive == 0 && prevActive > 0 && s.io.Vp != nil {
		// an empty frame writes nothing, so a viewport still holds the last
		// rows, and stopping would keep them in the scrollback
		if err := s.io.Clear(prevActive, s.io); err != nil {
			return currActive, fmt.Errorf("clear: %w", err)
		}
	}
	return currActive, nil
}

// drawRows writes lines to frame, one terminal row each. A terminal may draw a
// cluster wider than it is measured, and a row that wraps would leave the top
// row of the frame below the next clear, so the terminal clips rows instead; a
// viewport wraps the rows itself.
func (s *Spinners) drawRows(frame *bytes.Buffer, lines []ansi.Text) {
	clip := len(lines) > 0 && s.io.Vp == nil
	if clip {
		frame.WriteString(term.AutowrapOff)
	}
	for _, line := range lines {
		frame.WriteByte('\r')
		frame.Write(line)
		frame.WriteByte('\n')
		frame.WriteByte('\r')
	}
	if clip {
		frame.WriteString(term.AutowrapOn)
	}
}

// treeRow is a displayed spinner and the plain branches drawn in front of it.
type treeRow struct {
	offset int
	tree   string
}

// display lists the live spinners in the order of their rows: every top-level
// spinner followed by its children, depth first. A child hangs from a branch of
// its parent, joined by a branch glyph where siblings follow and an end glyph
// for the last one. Branches stop growing at cols columns, as a deeper row is
// clipped anyway.
func (s *Spinners) display(cols int) (rows []treeRow) {
	sym := s.theme.Source.Symbols
	type item struct {
		offset int
		lead   string
		last   bool
	}
	for root, spinner := range s.state {
		if spinner == nil || spinner.parent >= 0 {
			continue
		}
		for stack := []item{{offset: root, last: true}}; len(stack) > 0; {
			it := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			spinner := s.state[it.offset]
			row := treeRow{offset: it.offset}
			lead := ""
			if spinner.parent >= 0 {
				joint := sym.Branch
				if it.last {
					joint = sym.End
				}
				row.tree = it.lead + joint + " "
				lead = it.lead
				if cols > ansi.Text(lead).Width() {
					lead += "  "
					if !it.last {
						lead = it.lead + sym.Guide + " "
					}
				}
			}
			rows = append(rows, row)
			var live []int
			for _, c := range spinner.children {
				if s.state[c] != nil {
					live = append(live, c)
				}
			}
			for i := len(live) - 1; i >= 0; i-- {
				stack = append(stack, item{offset: live[i], lead: lead, last: i == len(live)-1})
			}
		}
	}
	return rows
}

// row renders a spinner through the slot of its state, after its branches.
// Only a running spinner advances its frame. The row is clipped before the
// last column, so that it cannot autowrap.
func (s *Spinners) row(spinner *spinnerState, tree string, cols int, now time.Time) (ansi.Text, error) {
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
		Elapsed: spinner.elapsedAt(now),
		Depth:   spinner.depth,
	})
	if err != nil {
		return nil, err
	}
	if tree != "" {
		branches, err := s.rows.tree.Render(tree)
		if err != nil {
			return nil, err
		}
		row = append(branches, row...)
	}
	if row.Width() > cols {
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
	_, ok := s.io.Out.(*term.ChanIO)
	if ok && s.io.Vp == nil {
		return
	}
	err := s.io.Clear(drawn, s.io)
	if err != nil {
		slog.Debug("clear spinners", "err", err)
	}
}

type createSpinner struct {
	cancel  context.CancelFunc
	frames  []string
	reply   chan createResult
	prefix  string
	keep    bool
	parent  int
	elapsed bool
}

// createResult answers a creation with the offset of the spinner or why it was
// rejected.
type createResult struct {
	offset int
	err    error
}

// newSpinner accepts a spinner, unless its parent has already ended.
func (s *Spinners) newSpinner(ns createSpinner) {
	offset := len(s.state)
	depth := 0
	if ns.parent >= 0 {
		parent := s.state[ns.parent]
		if parent == nil || parent.terminal() {
			s.respond(ns, createResult{err: ErrSpinnerStopped}) // the caller cancels
			return
		}
		depth = parent.depth + 1
		parent.children = append(parent.children, offset)
	}
	s.wg.Add(1)
	s.state = append(s.state, &spinnerState{
		cancel:  ns.cancel,
		Prefix:  ansi.Text(ns.prefix),
		tick:    (len(s.state) + 1) % len(ns.frames),
		frames:  ns.frames,
		active:  true,
		keep:    ns.keep,
		parent:  ns.parent,
		depth:   depth,
		elapsed: ns.elapsed,
		started: s.now(),
	})
	s.logStage(slog.LevelInfo, "spinner started", offset, "started")
	s.emit(spinnerAdded{
		Name:   ns.prefix,
		Index:  offset,
		Parent: ns.parent,
	})
	s.respond(ns, createResult{offset: offset})
}

func (s *Spinners) respond(ns createSpinner, res createResult) {
	select {
	case <-s.ctx.Done():
	case ns.reply <- res:
	}
}

// logStage runs on the group loop, preserving update order without logging animation frames.
func (s *Spinners) logStage(level slog.Level, message string, offset int, status string) {
	if s.io != nil {
		return
	}
	spinner := s.state[offset]
	attrs := []any{
		"spinner", offset,
		"prefix", spinner.Prefix.String(),
		"status", status,
	}
	if spinner.parent >= 0 {
		attrs = append(attrs, "parent", spinner.parent, "depth", spinner.depth)
	}
	slog.Default().Log(s.ctx, level, message, attrs...)
}
