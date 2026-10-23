// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package spinner

import (
	"context"
	"errors"
	"fmt"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/render"
)

// ErrSpinnerStopped is returned when a child is added to a spinner that has
// stopped, failed or been removed.
var ErrSpinnerStopped = errors.New("spinner stopped")

type spinnerState struct {
	cancel  context.CancelFunc
	tick    int
	active  bool
	keep    bool
	frames  []string
	Prefix  ansi.Text
	Message ansi.Text
	Failed  bool
	Done    bool

	// parent is the offset of the spinner this one is nested under, or -1.
	// Children are in the order they were accepted. Both are fixed at creation.
	parent   int
	depth    int
	children []int

	// elapsed shows the timer of an interactive row. The timer runs from the
	// creation of the spinner until its first terminal transition.
	elapsed bool
	started time.Time
	ended   time.Time
	frozen  bool
}

// terminal reports whether the spinner has failed or stopped, which are final.
func (ss *spinnerState) terminal() bool { return ss.Done || ss.Failed }

// freeze ends the timer at the first terminal transition only.
func (ss *spinnerState) freeze(now time.Time) {
	if ss.frozen {
		return
	}
	ss.frozen, ss.ended = true, now
}

// elapsedAt formats the time the spinner has been running at now, or "" when
// the timer is off.
func (ss *spinnerState) elapsedAt(now time.Time) string {
	if !ss.elapsed {
		return ""
	}
	if ss.frozen {
		now = ss.ended
	}
	return formatElapsed(now.Sub(ss.started))
}

// formatElapsed shows whole seconds as 12s, 1m05s or 1h00m00s. Hours do not wrap.
func formatElapsed(d time.Duration) string {
	secs := max(int64(d/time.Second), 0)
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%dh%02dm%02ds", secs/3600, secs/60%60, secs%60)
}

func (ss *spinnerState) next() {
	ss.tick = (ss.tick + 1) % len(ss.frames)
}

// spinnerView is the data of the spinner slots. Prefix and Message are
// caller text on one line; State is running, stopped or failed, as in the
// non-terminal logs. Elapsed is empty unless the timer is shown, and Depth is
// the nesting level, which the group already draws as branches.
type spinnerView struct {
	Frame   string
	Prefix  string
	Message string
	State   string
	Elapsed string
	Depth   int
}

// spinnerSlots are the spinner slots bound to the destination of a group.
type spinnerSlots struct{ running, stopped, failed, tree render.BoundSlot }

// bindSpinnerSlots attaches destination helpers to every spinner slot.
func bindSpinnerSlots(t render.SpinnerSlots, funcs template.FuncMap) (s spinnerSlots, _ error) {
	var errs [4]error
	s.tree, errs[3] = t.Tree.Bind(funcs)
	s.running, errs[0] = t.Running.Bind(funcs)
	s.stopped, errs[1] = t.Stopped.Bind(funcs)
	s.failed, errs[2] = t.Failed.Bind(funcs)
	return s, errors.Join(errs[:]...)
}

type Spinner struct {
	parent *Spinners
	offset int
}

// Add creates a spinner nested under this one. Closing or failing this spinner
// stops its running children, which also end with the group. A stopped or failed
// spinner returns [ErrSpinnerStopped].
func (s *Spinner) Add(ctx context.Context, opt ...option.Opt) (*Spinner, error) {
	return s.parent.add(ctx, s.offset, opt)
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
