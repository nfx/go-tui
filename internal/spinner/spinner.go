// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package spinner

import (
	"context"
	"errors"
	"fmt"
	"text/template"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/render"
)

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
type spinnerSlots struct{ running, stopped, failed render.BoundSlot }

// bindSpinnerSlots attaches destination helpers to every spinner slot.
func bindSpinnerSlots(t render.SpinnerSlots, funcs template.FuncMap) (s spinnerSlots, _ error) {
	var errs [3]error
	s.running, errs[0] = t.Running.Bind(funcs)
	s.stopped, errs[1] = t.Stopped.Bind(funcs)
	s.failed, errs[2] = t.Failed.Bind(funcs)
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
