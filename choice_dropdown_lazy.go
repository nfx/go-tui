// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"io"
	"iter"
)

type lazyResult struct {
	index       int
	done        bool
	needsRender bool
	readNextKey bool
}

// answerLazyFromDefault answers without a terminal. The producer starts only
// when a default or the single-item shortcut can answer, and is then consumed
// to the end, so that a later item cannot hide an ambiguity.
func (d *dropdown) answerLazyFromDefault(ctx context.Context, seq iter.Seq2[any, error]) (int, error) {
	err := ctx.Err()
	if err != nil {
		return -1, err
	}
	if !d.def.Set && !d.OneReturn {
		return -1, option.NoDefault(d.Label)
	}
	// consume through the producer channel so that cancellation returns
	// even while the iterator is blocked between yields
	d.startLazyProducer(ctx, seq)
	var items []any
loop:
	for {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case it, more := <-d.itItems:
			if !more {
				break loop
			}
			if it.err != nil {
				return -1, it.err
			}
			items = append(items, it.item)
		}
	}
	d.itItems = nil
	err = ctx.Err()
	if err != nil {
		return -1, err
	}
	if len(items) == 0 {
		return -1, ErrEmptyLazyResult
	}
	d.Items = items
	i, err := d.resolveAutomatic(items)
	if err != nil {
		return -1, err
	}
	return i, ctx.Err()
}

// startLazyProducer streams itemFn into the dropdown until it is done
// or ctx is cancelled. A yield blocked on a full channel observes ctx, but
// iterator work that neither yields nor checks a context is not interrupted.
func (d *dropdown) startLazyProducer(ctx context.Context, itemFn iter.Seq2[any, error]) {
	ch := make(chan itPair, max(1, d.IterBatchSize))
	d.itItems = ch // the dropdown may later set its own field to nil
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		default:
		}
		for v, err := range itemFn {
			select {
			case <-ctx.Done():
				return
			case ch <- itPair{v, err}:
				if err != nil {
					return
				}
			}
		}
	}()
}

// runLazy renders the dropdown while items are streamed in.
func (d *dropdown) runLazy(ctx context.Context, tio *term.IO, frame *bytes.Buffer) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	keys := tio.ReadEvents(ctx, nil)
	defer func() {
		cancel()
		// wait without draining, so an unreceived key stays for the next prompt
		tio.AwaitReader()
	}()
	space := 0
	displayed := 0
	needsRender := true
	for {
		var err error
		err = d.ensureLazyRender(tio, frame, &needsRender, &space, &displayed)
		if err != nil {
			return -1, err
		}
		res, err := d.nextLazyAction(tio, frame, space, displayed, keys)
		if err != nil {
			return -1, err
		}
		if res.done {
			return res.index, nil
		}
		if res.readNextKey {
			keys = tio.ReadEvents(ctx, nil)
		}
		needsRender = res.needsRender
	}
}

// ensureLazyRender draws the dropdown only when it needs a refresh.
func (d *dropdown) ensureLazyRender(
	tio *term.IO,
	frame *bytes.Buffer,
	needsRender *bool,
	space *int,
	displayed *int,
) error {
	if !*needsRender {
		return nil
	}
	err := d.renderLazyFrame(tio, frame, space, displayed)
	if err != nil {
		return err
	}
	*needsRender = false
	return nil
}

// nextLazyAction waits for item, key, or context updates.
func (d *dropdown) nextLazyAction(
	tio *term.IO,
	frame *bytes.Buffer,
	space int,
	displayed int,
	keys <-chan term.KeyEvent,
) (lazyResult, error) {
	select {
	case it, more := <-d.itItems:
		i, nextRender, err := d.handleLazyItem(tio, frame, space, it, more)
		if err != nil {
			return lazyResult{}, err
		}
		return lazyResult{index: i, done: i >= 0, needsRender: nextRender}, nil
	case ev, ok := <-keys:
		return d.nextLazyKeyResult(tio, frame, space, displayed, ev, ok)
	case ev, ok := <-d.input:
		i, nextRender, err := d.handleLazyInput(tio, frame, space, displayed, ev, ok)
		if err != nil {
			return lazyResult{}, err
		}
		return lazyResult{index: i, done: i >= 0, needsRender: nextRender}, nil
	case <-d.Ctx.Done():
		err := d.clearFrame(tio, frame, space)
		if err != nil {
			return lazyResult{}, err
		}
		return lazyResult{}, d.Ctx.Err()
	case <-tio.OnResize:
		return lazyResult{needsRender: true}, nil
	}
}

func (d *dropdown) nextLazyKeyResult(
	tio *term.IO,
	frame *bytes.Buffer,
	space int,
	displayed int,
	ev term.KeyEvent,
	ok bool,
) (lazyResult, error) {
	i, nextRender, err := d.handleLazyKey(tio, frame, space, displayed, ev, ok)
	if err != nil {
		return lazyResult{}, err
	}
	return lazyResult{
		index:       i,
		done:        i >= 0,
		needsRender: nextRender,
		readNextKey: i < 0,
	}, nil
}

// renderLazyFrame clears the previous output and renders the dropdown.
func (d *dropdown) renderLazyFrame(tio *term.IO, frame *bytes.Buffer, space *int, displayed *int) error {
	if *space > 0 {
		err := tio.Clear(*space, frame)
		if err != nil {
			return fmt.Errorf("clear: %w", err)
		}
	}
	err := d.render(tio, frame)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	_, err = frame.WriteTo(tio)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	*space = d.height()
	*displayed = len(d.displayed)
	return nil
}

func (d *dropdown) finishLazyItems(tio *term.IO, frame *bytes.Buffer, space int) (int, bool, error) {
	d.iterDone = true
	d.itItems = nil
	if len(d.Items) == 0 {
		clearErr := d.clearFrame(tio, frame, space)
		if clearErr != nil {
			return -1, false, errors.Join(io.EOF, clearErr)
		}
		return -1, false, ErrEmptyLazyResult
	}
	err := d.validateLazySeed()
	if err != nil {
		clearErr := d.clearFrame(tio, frame, space)
		if clearErr != nil {
			return -1, false, errors.Join(err, clearErr)
		}
		return -1, false, err
	}
	if d.OneReturn && len(d.relevant) == 1 {
		d.emit(dropdownConfirmed{Selected: d.relevant[0]})
		err := d.clearFrame(tio, frame, space)
		if err != nil {
			return -1, false, err
		}
		return 0, false, nil
	}
	return -1, false, nil
}

// validateLazySeed checks a typed default against the complete sequence: it
// must select exactly one entry, however many arrived after the first match.
func (d *dropdown) validateLazySeed() error {
	if !d.seedLazy {
		return nil
	}
	matches, err := d.target.matches(d.def.Raw, d.Items)
	if err != nil {
		return err
	}
	i, err := uniqueMatch(matches, fmt.Sprintf("%T value", d.def.Raw))
	if err != nil {
		return err
	}
	d.seedIdx, d.seeded = i, true
	return nil
}

// handleLazyItem updates the dropdown for a streamed item.
func (d *dropdown) handleLazyItem(
	tio *term.IO,
	frame *bytes.Buffer,
	space int,
	it itPair,
	more bool,
) (int, bool, error) {
	if !more {
		return d.finishLazyItems(tio, frame, space)
	}
	if it.err != nil {
		clearErr := d.clearFrame(tio, frame, space)
		if clearErr != nil {
			return -1, false, errors.Join(it.err, clearErr)
		}
		return -1, false, it.err
	}
	err := d.addItem(tio.Height, it.item)
	if err != nil {
		clearErr := d.clearFrame(tio, frame, space)
		if clearErr != nil {
			return -1, false, errors.Join(err, clearErr)
		}
		return -1, false, err
	}
	return -1, true, nil
}

// handleLazyInput updates the dropdown from a semantic input event.
func (d *dropdown) handleLazyInput(
	tio *term.IO,
	frame *bytes.Buffer,
	space int,
	displayed int,
	ev dropdownInputEvent,
	ok bool,
) (int, bool, error) {
	if !ok {
		err := d.clearFrame(tio, frame, space)
		if err != nil {
			return -1, false, errors.Join(io.EOF, err)
		}
		return -1, false, io.EOF
	}
	i := d.applyInputEvent(tio, ev, displayed, space)
	if i >= 0 {
		err := d.clearFrame(tio, frame, space)
		if err != nil {
			return -1, false, err
		}
		return i, false, nil
	}
	return -1, true, nil
}

// handleLazyKey updates the dropdown for a key event.
func (d *dropdown) handleLazyKey(
	tio *term.IO,
	frame *bytes.Buffer,
	space int,
	displayed int,
	ev term.KeyEvent,
	ok bool,
) (int, bool, error) {
	if !ok {
		err := d.clearFrame(tio, frame, space)
		if err != nil {
			return -1, false, errors.Join(io.EOF, err)
		}
		return -1, false, io.EOF
	}
	if ev.Err != nil {
		if term.IsPasteText(ev.Err) {
			return -1, false, nil
		}
		readErr := fmt.Errorf("read: %w", ev.Err)
		clearErr := d.clearFrame(tio, frame, space)
		if clearErr != nil {
			return -1, false, errors.Join(readErr, clearErr)
		}
		return -1, false, readErr
	}
	i := d.pressKeyRune(tio, ev.Key, displayed, space)
	if i >= 0 {
		err := d.clearFrame(tio, frame, space)
		if err != nil {
			return -1, false, err
		}
		return i, false, nil
	}
	return -1, true, nil
}
