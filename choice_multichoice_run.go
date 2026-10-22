// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"io"
	"strings"
	"unicode"
)

// handleKey changes state only; returning true confirms the checked collection.
func (m *multichoice) handleKey(key rune) bool {
	switch key {
	case keyEnter:
		return !m.loading
	case '↑', '↓':
		m.manualFocus = true
		pos := m.offset + m.active
		if key == '↑' && pos > 0 {
			m.active--
		}
		if key == '↓' && pos+1 < len(m.relevant) {
			m.active++
		}
	case ' ':
		i := m.focused()
		if i < 0 {
			return false
		}
		if m.selected[i] {
			m.toggled[i] = true
			m.selected[i] = false
			m.count--
		} else {
			m.toggled[i] = true
			m.selected[i] = true
			m.count++
		}
	case 0x7f, 0x08:
		clusters := ansi.Text(m.typed).Clusters()
		if len(clusters) > 0 {
			runes := []rune(m.typed)
			m.filter(string(runes[:clusters[len(clusters)-1].Start]))
		}
	case keyEscape:
		if m.typed != "" {
			m.filter("")
		}
	default:
		if !unicode.IsControl(key) && key != '←' && key != '→' {
			m.filter(m.typed + string(key))
		}
	}
	return false
}

// paste appends filter text without interpreting pasted controls as prompt actions.
func (m *multichoice) paste(payload []byte) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t':
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, string(payload))
	m.filter(m.typed + clean)
}

// startProducer bounds queued arrivals and lets cancellation unblock publication.
func (m *multichoice) startProducer(ctx context.Context) <-chan itPair {
	if m.producer == nil {
		return nil
	}
	ch := make(chan itPair, 16)
	go func() {
		defer close(ch)
		if ctx.Err() != nil {
			return
		}
		m.producer(func(item any, err error) bool {
			if ctx.Err() != nil {
				return false
			}
			select {
			case <-ctx.Done():
				return false
			case ch <- itPair{item, err}:
				return err == nil
			}
		})
	}()
	return ch
}

// finishLoading resolves the final default and requires a newly arrived Enter to confirm.
func (m *multichoice) finishLoading() error {
	m.loading = false
	if len(m.Items) == 0 {
		return ErrEmptyLazyResult
	}
	err := m.resolveDefault()
	if err != nil {
		return err
	}
	m.readyOrder = nextInputOrder()
	return nil
}

// run emits an answer only after successful interaction and cleanup.
func (m *multichoice) run(ctx context.Context, o ...opt) error {
	if err := m.prepare(o); err != nil {
		return err
	}
	if m.in == nil || m.out == nil {
		in, out := defaultStreams(ctx)
		if m.in == nil {
			m.in = in
		}
		if m.out == nil {
			m.out = out
		}
	}
	if !outputIsTerminal(m.out) {
		return m.answerFromDefault(ctx) // an automatic answer prints nothing
	}
	if err := m.interact(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.showAnswer()
}

// interact owns terminal modes and frame cleanup, preserving both interaction and cleanup errors.
func (m *multichoice) interact(parent context.Context) (resultErr error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	if m.in == nil || m.out == nil {
		in, out := defaultStreams(parent)
		if m.in == nil {
			m.in = in
		}
		if m.out == nil {
			m.out = out
		}
	}
	tio, err := m.makeTermIO(m.in, m.out)
	if err != nil {
		return fmt.Errorf("raw term: %w", err)
	}
	tio.splitKeys = true
	tio.bracketedPaste = true
	space := 0
	pasteMode := false
	defer func() {
		cancel()
		tio.awaitReader()
		if err := m.clearMultichoiceFrame(tio, space); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clear: %w", err))
		}
		if pasteMode {
			if err := m.setPasteMode(tio, false); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("paste mode: %w", err))
			}
		}
		if err := tio.Restore(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("restore: %w", err))
		}
	}()
	// before the event reader starts, as binding may probe the background
	if err := m.bindRun(ctx, tio); err != nil {
		return err
	}
	var frame bytes.Buffer
	if err := m.render(tio, &frame); err != nil {
		return fmt.Errorf("render: %w", err)
	}
	if m.interactiveInput(tio) {
		pasteMode = true
		if err := m.setPasteMode(tio, true); err != nil {
			return fmt.Errorf("paste mode: %w", err)
		}
	}
	space = bytes.Count(frame.Bytes(), []byte{'\n'})
	if err := m.writeMultichoiceFrame(tio, &frame); err != nil {
		return fmt.Errorf("frame write: %w", err)
	}
	return m.readLoop(ctx, tio, &frame, &space)
}

// readLoop is the sole owner of item, focus, and checked state.
func (m *multichoice) readLoop(ctx context.Context, tio *termIO, frame *bytes.Buffer, space *int) error {
	arrivals := m.startProducer(ctx)
	keys := tio.readEvents(ctx, nil)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tio.onResize:
		case pair, ok := <-arrivals:
			if !ok {
				arrivals = nil
				if err := m.finishLoading(); err != nil {
					return err
				}
			} else {
				if pair.err != nil {
					return fmt.Errorf("producer: %w", pair.err)
				}
				if err := m.addItem(pair.item); err != nil {
					return err
				}
			}
		case ev, ok := <-keys:
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return io.EOF
			}
			done, err := m.acceptEvent(tio, ev)
			if err != nil || done {
				return err
			}
			keys = tio.readEvents(ctx, nil)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.redraw(tio, frame, space); err != nil {
			return err
		}
	}
}

// acceptEvent separates paste from keys and ignores Enter received before loading finished.
func (m *multichoice) acceptEvent(tio *termIO, ev keyEvent) (bool, error) {
	switch {
	case ev.bracketed:
		m.paste(ev.paste)
	case isPasteText(ev.err):
		tio.replayKeys(ev)
	case ev.err != nil:
		return false, fmt.Errorf("input: %w", ev.err)
	case ev.key == keyEnter && m.readyOrder != 0 && ev.readOrder < m.readyOrder:
		return false, nil
	default:
		return m.handleKey(ev.key), nil
	}
	return false, nil
}

// redraw replaces the prior frame and tracks its new height for cleanup.
func (m *multichoice) redraw(tio *termIO, frame *bytes.Buffer, space *int) error {
	frame.Reset()
	if *space > 0 {
		if err := tio.clear(*space, frame); err != nil {
			return fmt.Errorf("clear: %w", err)
		}
	}
	start := frame.Len()
	if err := m.render(tio, frame); err != nil {
		return fmt.Errorf("render: %w", err)
	}
	*space = bytes.Count(frame.Bytes()[start:], []byte{'\n'})
	if err := m.writeMultichoiceFrame(tio, frame); err != nil {
		return fmt.Errorf("frame write: %w", err)
	}
	return nil
}

// interactiveInput limits terminal paste-mode changes to interactive readers.
func (m *multichoice) interactiveInput(t *termIO) bool {
	in, ok := t.in.(descriptor)
	return ok && terminalInputChecker(int(in.Fd()))
}

// setPasteMode flushes terminal mode changes before reading or restoring.
func (m *multichoice) setPasteMode(t *termIO, enabled bool) error {
	mode := "\x1b[?2004l"
	if enabled {
		mode = "\x1b[?2004h"
	}
	n, err := io.WriteString(t.out, mode)
	if err == nil && n != len(mode) {
		err = io.ErrShortWrite
	}
	if err == nil && t.cio != nil {
		err = t.cio.Flush()
	}
	return err
}
