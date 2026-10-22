// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/render"
	"io"
	"unicode/utf8"
)

type input struct {
	Config

	Label    string
	Hide     bool
	Password bool

	// theme is the snapshot captured at construction. The prompt renders
	// through it, and run binds it to the destination: look holds the
	// binding, label the label rendered once with it, answer and feedback the
	// bound slots of the prompt family.
	theme            *render.Compiled
	look             render.Binding
	label, hint      ansi.Text
	answer, feedback render.BoundSlot
	// numeric selects the Number slots of the theme.
	numeric bool
	// keys are the bindings beyond editing that the hint row describes.
	keys []render.Hint

	def DefaultValue
	// rules validate the confirmed text in order; the first error rejects it.
	rules []func(context.Context, string) error
	// rejection is the message of the last rejected confirmation.
	rejection string
	// adjust optionally steps on Up/Down; errors become rejections.
	adjust func(key rune) error

	// rows is the height of the last drawn frame: the input and optional
	// feedback, below a guided label. caretRow is the row of the caret in it.
	rows     int
	caretRow int
	typed    string
	cursor   int

	// TODO: special case for testing?..
	makeTermIO func(in io.Reader, out io.Writer) (*termIO, error)
	eventSink  func(inputOutgoing)
	input      <-chan inputIncoming
}

// implement [withPrompt] interface.
func (i *input) SetHide() {
	i.Hide = true
}

// implement [withPrompt] interface.
func (i *input) SetDefault(d any) {
	i.def = DefaultValue{set: true, raw: d}
	i.typed = fmt.Sprint(d)
	i.cursor = utf8.RuneCountInString(i.typed)
}

func newInput(ctx context.Context, label string) *input {
	in, out := defaultStreams(ctx)
	return &input{
		ctx:        ctx,
		out:        out,
		in:         in,
		Label:      label,
		theme:      defaultIO.currentTheme(),
		makeTermIO: makePromptIO,
	}
}

func (i *input) read(ctx context.Context, option ...Opt) (string, error) {
	err := Opts(option).Apply(i)
	if err != nil {
		return "", err
	}
	if !outputIsTerminal(i.out) {
		return i.answerFromDefault(ctx)
	}
	out, err := i.run(ctx)
	if err != nil {
		return "", err
	}
	err = i.showAnswer()
	if err != nil {
		return "", err
	}
	return out, nil
}

// answerFromDefault answers without a terminal: it reads no input and prints
// nothing. The default goes through the rules once, and a rejection is an error.
func (i *input) answerFromDefault(ctx context.Context) (string, error) {
	err := ctx.Err()
	if err != nil {
		return "", err
	}
	if !i.def.set {
		return "", NoDefault(i.Label)
	}
	for _, rule := range i.rules {
		err = rule(ctx, i.typed)
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		if err != nil {
			// the text itself stays out of the error, it may be a secret
			return "", BadDefault("is rejected: %w", err)
		}
	}
	return i.typed, nil
}

func (i *input) showAnswer() error {
	if i.Hide || i.Password {
		return nil
	}
	var buf bytes.Buffer
	err := i.themedAnswer(&buf)
	if err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	_, err = buf.WriteTo(i.out)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// run renders the prompt and stops its event goroutine before restoring the terminal.
func (p *input) run(ctx context.Context) (string, error) {
	io, err := p.makeTermIO(p.in, p.out)
	if err != nil {
		return "", err
	}
	defer io.Restore() //nolint:errcheck
	// before the event reader starts, as it may probe the background
	err = p.bind(ctx, io)
	if err != nil {
		return "", err
	}
	frame := &bytes.Buffer{}
	// Cancel the event goroutine on return; an outstanding wrapper read is
	// retained for the next prompt on this input (see termIO.startRead).
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var keys <-chan keyEvent
	var permit chan<- struct{}
	if p.input == nil {
		permits := make(chan struct{}, 1)
		permit = permits
		// without a paste boundary, buffered bytes are keys typed together
		io.splitKeys = true
		keys = io.readEvents(runCtx, permits)
		defer func() {
			cancel()
			io.awaitReader()
		}()
		// authorize the first read. Only a consumed key authorizes the next one,
		// so neither a resize nor a confirmation lets the reader run ahead.
		permit <- struct{}{}
	}
	p.emit(inputInit{
		Label:    p.Label,
		Password: p.Password,
	})
	for {
		err = p.render(io, frame)
		if err != nil {
			return "", errors.Join(err, p.clear(io))
		}
		done, consumed, err := p.handleNextEvent(ctx, io, keys)
		if err != nil {
			return "", err
		}
		if done {
			return p.typed, p.clear(io)
		}
		if consumed && permit != nil {
			// the reader took the previous permit before sending the key,
			// so the slot is free and this send does not block.
			permit <- struct{}{}
		}
	}
}

// handleNextEvent waits for the next key or input event and applies it to the input state.
// It reports whether a key was consumed from keys.
func (p *input) handleNextEvent(
	ctx context.Context,
	tio *termIO,
	keys <-chan keyEvent,
) (done, consumed bool, err error) {
	select {
	case <-p.ctx.Done():
		return false, false, errors.Join(p.ctx.Err(), p.clear(tio))
	case ev, ok := <-keys:
		if !ok && p.ctx.Err() != nil {
			// the reader closed keys because of the cancellation
			return false, false, errors.Join(p.ctx.Err(), p.clear(tio))
		}
		// key press handlers has to be limited to state updates, not writes to the buffer.
		done, err := p.handleKeyEvent(ctx, ev, ok)
		if err != nil { // e.g., Ctrl+C or Ctrl+D
			return false, ok, errors.Join(err, p.clear(tio))
		}
		return done, ok, nil
	case ev, ok := <-p.input:
		done, err := p.handleInputEvent(ev, ok)
		if err != nil {
			return false, false, errors.Join(err, p.clear(tio))
		}
		return done, false, nil
	case <-tio.onResize:
		// resize triggers a re-render with updated geometry
		return false, false, nil
	}
}

func (p *input) handleKeyEvent(ctx context.Context, ev keyEvent, ok bool) (bool, error) {
	if !ok {
		return false, fmt.Errorf("read: %w", io.EOF)
	}
	if isPasteText(ev.err) {
		// a read started by another widget may report buffered keys as a paste
		return p.handleBufferedKeys(ctx, ev.paste)
	} else if ev.err != nil {
		// Ctrl+C or Ctrl+D will result in an error like io.EOF
		return false, fmt.Errorf("read: %w", ev.err)
	}
	switch ev.key {
	case '←':
		p.pressLeft()
		return false, nil
	case '→':
		p.pressRight()
		return false, nil
	case '↑', '↓':
		if p.adjust != nil {
			p.reject(p.adjust(ev.key))
		}
		return false, nil
	case keyEscape, keyIgnored: // ignore other special keys
		return false, nil
	}
	return p.applyInputEvent(p.decodeInputEvent(ev.key)), nil
}

// handleBufferedKeys applies keys returned together by one read in order, so
// that Enter or backspace keep their meaning. A paste cannot be told apart from
// them, as there is no paste boundary. Keys after a confirming Enter are dropped.
func (p *input) handleBufferedKeys(ctx context.Context, buf []byte) (bool, error) {
	decoder := &termIO{in: bytes.NewReader(nil), pending: buf, splitKeys: true}
	for len(decoder.pending) > 0 {
		key, _, err := decoder.readRune(ctx)
		done, err := p.handleKeyEvent(ctx, keyEvent{key: key, err: err}, true)
		if done || err != nil {
			return done, err
		}
	}
	return false, nil
}

func (p *input) handleInputEvent(ev inputIncoming, ok bool) (bool, error) {
	if !ok {
		return false, fmt.Errorf("read: %w", io.EOF)
	}
	return p.applyInputEvent(ev), nil
}

func (p *input) emit(ev inputOutgoing) {
	if p.eventSink == nil {
		return
	}
	p.eventSink(ev)
}

// decodeInputEvent maps one terminal key to an input change or confirmation.
func (p *input) decodeInputEvent(key rune) inputIncoming {
	switch key {
	case keyEnter:
		return inputConfirmed{}
	case 0x7f: // backspace
		runes := []rune(p.typed)
		cursor := min(p.cursor, len(runes))
		if cursor > 0 {
			from := p.prevBoundary(cursor)
			runes = append(runes[:from], runes[cursor:]...)
			cursor = from
		}
		return inputChanged{
			Text:   string(runes),
			Cursor: &cursor,
		}
	default:
		return p.insertAtCursor(string(key))
	}
}

func (p *input) applyInputEvent(ev inputIncoming) bool {
	switch typed := ev.(type) {
	case inputChanged:
		p.applyInputChanged(typed)
	case inputConfirmed:
		return p.confirmInput()
	}
	return false
}

// applyInputChanged updates text and caret, clearing rejection feedback only when text changes.
func (p *input) applyInputChanged(ev inputChanged) {
	prevLen := utf8.RuneCountInString(p.typed)
	prevCursor := p.cursor
	if ev.Text != p.typed {
		p.rejection = ""
	}
	p.typed = ev.Text
	nextLen := utf8.RuneCountInString(p.typed)
	if ev.Cursor != nil {
		p.cursor = min(max(*ev.Cursor, 0), nextLen)
		return
	}
	switch {
	case prevLen+1 == nextLen:
		if prevCursor < nextLen {
			p.cursor = prevCursor + 1
		} else {
			p.cursor = nextLen
		}
	case prevLen == nextLen+1:
		if prevCursor > 0 {
			p.cursor = prevCursor - 1
		} else {
			p.cursor = 0
		}
	default:
		p.cursor = nextLen
	}
}

// confirmInput accepts the typed text when every rule passes. A rejection keeps
// the text and shows the reason; cancellation is reported by the event loop.
func (p *input) confirmInput() bool {
	for _, rule := range p.rules {
		if p.ctx.Err() != nil {
			return false
		}
		err := rule(p.ctx, p.typed)
		if p.ctx.Err() != nil {
			return false
		}
		if err != nil {
			p.reject(err)
			return false
		}
	}
	p.rejection = ""
	p.emit(inputComplete{Value: p.typed})
	return true
}

// reject records non-nil errors, hiding custom messages for passwords.
func (p *input) reject(err error) {
	if err == nil {
		return
	}
	p.rejection = err.Error()
	var rule ruleError
	if p.rejection == "" || p.Password && !errors.As(err, &rule) {
		p.rejection = "Invalid value"
	}
}
