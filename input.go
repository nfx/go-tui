// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/template"
	"unicode/utf8"
)

type inputOutgoing interface {
	isInputOutgoing()
}

type inputOutgoingMarker struct{}

func (inputOutgoingMarker) isInputOutgoing() {}

type inputInit struct {
	inputOutgoingMarker
	Label    string
	Password bool
}

type inputComplete struct {
	inputOutgoingMarker
	Value string
}

type inputIncoming interface {
	isInputIncoming()
}

type inputIncomingMarker struct{}

func (inputIncomingMarker) isInputIncoming() {}

type inputChanged struct {
	inputIncomingMarker
	Text string
}

type inputConfirmed struct {
	inputIncomingMarker
}

type inputKeyEvent struct {
	key   rune
	err   error
	paste []byte
}

func WithDefault(d string) opt {
	return func(a any) error {
		switch p := a.(type) {
		case *input:
			p.typed = d
			p.cursor = len(d)
			return nil
		case *dropdown:
			p.oneMatch = d
			return nil
		default:
			return fmt.Errorf("%w: need a input, got %v", ErrInvalidState, a)
		}
	}
}

// WithNonEmpty forces the prompt to continue until the confirmed input is not empty.
func WithNonEmpty() opt {
	return inputOpt(func(i *input) error {
		prev := i.CheckFn
		i.CheckFn = func(rawInput string) (string, bool) {
			if prev != nil {
				next, ok := prev(rawInput)
				if !ok {
					return next, false
				}
				rawInput = next
			}
			return rawInput, rawInput != ""
		}
		return nil
	})
}

func inputOpt(o func(d *input) error) opt {
	return func(a any) error {
		d, ok := a.(*input)
		if !ok {
			return fmt.Errorf("%w: need a input, got %v", ErrInvalidState, a)
		}
		return o(d)
	}
}

type input struct {
	config

	Label    string
	Hide     bool
	Password bool

	LabelTemplate  string
	labelTemplate  *template.Template
	AnswerTemplate string
	answerTemplate *template.Template
	typed          string
	cursor         int

	CheckFn func(rawInput string) (string, bool)

	// TODO: special case for testing?..
	makeTermIO func(in io.Reader, out io.Writer) (*termIO, error)
	eventSink  func(inputOutgoing)
	input      <-chan inputIncoming
}

func newInput(label string) *input {
	return &input{
		ctx:            context.Background(),
		out:            defaultOutput(),
		in:             defaultInput(),
		Label:          label,
		LabelTemplate:  DefaultLabelTemplate,
		AnswerTemplate: DefaultAnswerTemplate,
		makeTermIO:     makeTermIO,
	}
}

func Input(label string, option ...opt) (string, error) {
	i := newInput(label)
	return i.read(option...)
}

func Password(label string, option ...opt) (string, error) {
	i := newInput(label)
	i.Password = true
	return i.read(option...)
}

func (i *input) read(option ...opt) (string, error) {
	err := opts(option).Apply(i)
	if err != nil {
		return "", err
	}
	err = i.parseTemplates()
	if err != nil {
		return "", err
	}
	out, err := i.run()
	if err != nil {
		return "", err
	}
	err = i.showAnswer()
	if err != nil {
		return "", err
	}
	return out, nil
}

func (i *input) showAnswer() error {
	if i.Hide || i.Password {
		return nil
	}
	var buf bytes.Buffer
	// TODO: unify reused structures
	err := i.answerTemplate.Execute(&buf, dropdownAnswer{
		Label:  i.Label,
		Answer: i.typed,
	})
	if err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	_, err = buf.WriteTo(i.out)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (p *input) run() (string, error) {
	io, err := p.makeTermIO(p.in, p.out)
	if err != nil {
		return "", err
	}
	defer io.Restore() //nolint:errcheck
	frame := &bytes.Buffer{}
	// cancel the reader when run() returns so it cannot steal stdin bytes
	// from a subsequent prompt (see readEvents for the permit pattern).
	runCtx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	var keys <-chan inputKeyEvent
	var permit chan<- struct{}
	if p.input == nil {
		keys, permit = p.readEvents(runCtx, io)
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
		// authorize the reader to fetch the next chunk; a 1-slot buffer with
		// non-blocking send caps outstanding reads at one.
		if permit != nil {
			select {
			case permit <- struct{}{}:
			default:
			}
		}
		done, err := p.handleNextEvent(io, keys)
		if err != nil {
			return "", err
		}
		if done {
			return p.typed, p.clear(io)
		}
	}
}

// handleNextEvent waits for the next key or input event and applies it to the input state.
func (p *input) handleNextEvent(tio *termIO, keys <-chan inputKeyEvent) (bool, error) {
	select {
	case <-p.ctx.Done():
		return false, errors.Join(p.ctx.Err(), p.clear(tio))
	case ev, ok := <-keys:
		// key press handlers has to be limited to state updates, not writes to the buffer.
		done, err := p.handleKeyEvent(ev, ok)
		if err != nil { // e.g., Ctrl+C or Ctrl+D
			return false, errors.Join(err, p.clear(tio))
		}
		return done, nil
	case ev, ok := <-p.input:
		done, err := p.handleInputEvent(ev, ok)
		if err != nil {
			return false, errors.Join(err, p.clear(tio))
		}
		return done, nil
	case <-tio.onResize:
		// resize triggers a re-render with updated geometry
		return false, nil
	}
}

// visibleWindow returns the start and end rune indexes for
// the visible portion of text, anchored around the cursor.
func (i *input) visibleWindow(runes []rune, availW int) (int, int) {
	total := len(runes)
	if availW <= 0 || total <= availW {
		return 0, total
	}
	if i.cursor <= availW/2 {
		return 0, availW
	}
	if i.cursor >= total-availW/2 {
		return total - availW, total
	}
	start := i.cursor - availW/2
	return start, start + availW
}

func (i *input) render(io *termIO, frame *bytes.Buffer) error {
	io.refreshSize()
	frame.Reset()
	var errs []error
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	collect(frame.WriteByte('\r'))
	// render label into a scratch buffer to measure its width
	var labelBuf bytes.Buffer
	collect(i.labelTemplate.Execute(&labelBuf, i.Label))
	labelW := width(labelBuf.Bytes())
	_, err := frame.Write(labelBuf.Bytes())
	collect(err)
	displayed := i.typed
	if i.Password {
		displayed = strings.Repeat("*", utf8.RuneCountInString(i.typed))
	}
	// clip text to a visible window that fits on one row
	runes := []rune(displayed)
	var visStart, visEnd int
	var visibleText string
	var visCursor int
	if io.Width > 0 {
		availW := io.Width - labelW
		if availW < 1 {
			availW = 1
		}
		visStart, visEnd = i.visibleWindow(runes, availW)
		visibleText = string(runes[visStart:visEnd])
		visCursor = i.cursor - visStart
	} else {
		visibleText = displayed
		visCursor = i.cursor
	}
	// write displayed text and clear to the end of the line
	_, err = fmt.Fprintf(frame, "%s\x1b[K", visibleText)
	collect(err)
	moveLeft := utf8.RuneCountInString(visibleText) - visCursor
	if moveLeft > 0 {
		// move the cursor left by the difference between
		// the end and the desired position
		_, err = fmt.Fprintf(frame, "\x1b[%dD", moveLeft)
		collect(err)
	}
	_, err = frame.WriteTo(io)
	collect(err)
	return errors.Join(errs...)
}

func (*input) clear(io *termIO) error {
	var errs []error
	_, err := fmt.Fprintln(io)
	if err != nil {
		errs = append(errs, fmt.Errorf("newline: %w", err))
	}
	err = io.clear(1, io)
	if err != nil {
		errs = append(errs, fmt.Errorf("clear: %w", err))
	}
	return errors.Join(errs...)
}

func (p *input) pressKey(io *termIO) (string, error) {
	key, n, err := io.ReadRune()
	ev := inputKeyEvent{
		key: key,
		err: err,
	}
	more, ok := errors.AsType[*pasteTextError](err)
	if ok {
		ev.paste = append(ev.paste, more.buf[:n]...)
	}
	done, err := p.handleKeyEvent(ev, true)
	if err != nil {
		return "", err
	}
	if done {
		return p.typed, nil
	}
	return "", nil
}

// readEvents launches a goroutine that reads one stdin chunk per granted permit
// and forwards it on the returned channel. The permit gate ensures the reader
// is parked (not in a syscall) once the consumer stops granting, so it cannot
// consume bytes destined for a subsequent prompt after run() returns.
func (p *input) readEvents(ctx context.Context, io *termIO) (<-chan inputKeyEvent, chan<- struct{}) {
	keys := make(chan inputKeyEvent)
	permit := make(chan struct{}, 1)
	go func() {
		defer close(keys)
		for {
			select {
			case <-ctx.Done():
				return
			case <-permit:
			}
			key, n, err := io.ReadRune()
			ev := inputKeyEvent{
				key: key,
				err: err,
			}
			var more *pasteTextError
			if errors.As(err, &more) {
				ev.paste = append(ev.paste, more.buf[:n]...)
			}
			select {
			case <-ctx.Done():
				return
			case keys <- ev:
			}
			if err != nil && more == nil {
				return
			}
		}
	}()
	return keys, permit
}

func (p *input) handleKeyEvent(ev inputKeyEvent, ok bool) (bool, error) {
	if !ok {
		return false, fmt.Errorf("read: %w", io.EOF)
	}
	_, ok = errors.AsType[*pasteTextError](ev.err)
	if ok {
		// Ctrl+V or CMD+V will just send more bytes. So we emulate typing.
		// This currently works with empty input only. Or appending to the end.
		// There's a bug when you paste in the middle of the text.
		for _, b := range ev.paste {
			done := p.applyInputEvent(p.decodeInputEvent(rune(b)))
			if done {
				return true, nil
			}
		}
		return false, nil
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
	case '↑', '↓': // ignore up/down arrows
		return false, nil
	}
	return p.applyInputEvent(p.decodeInputEvent(ev.key)), nil
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

func (p *input) decodeInputEvent(key rune) inputIncoming {
	switch key {
	case keyEnter:
		return inputConfirmed{}
	case 0x7f: // backspace
		nextText := p.typed
		if len(nextText) > 0 && p.cursor > 0 {
			nextText = nextText[:p.cursor-1] + nextText[p.cursor:]
		}
		return inputChanged{
			Text: nextText,
		}
	default:
		nextText := p.typed[:p.cursor] + string(key) + p.typed[p.cursor:]
		return inputChanged{
			Text: nextText,
		}
	}
}

func (p *input) applyInputEvent(ev inputIncoming) bool {
	switch typed := ev.(type) {
	case inputChanged:
		p.applyInputChanged(typed.Text)
	case inputConfirmed:
		return p.confirmInput()
	}
	return false
}

func (p *input) applyInputChanged(text string) {
	prevLen := len(p.typed)
	prevCursor := p.cursor
	p.typed = text
	switch {
	case prevLen+1 == len(p.typed):
		if prevCursor < len(p.typed) {
			p.cursor = prevCursor + 1
		} else {
			p.cursor = len(p.typed)
		}
	case prevLen == len(p.typed)+1:
		if prevCursor > 0 {
			p.cursor = prevCursor - 1
		} else {
			p.cursor = 0
		}
	case prevCursor > len(p.typed):
		p.cursor = len(p.typed)
	default:
		p.cursor = len(p.typed)
	}
}

func (p *input) confirmInput() bool {
	if p.CheckFn != nil {
		next, ok := p.CheckFn(p.typed)
		if !ok {
			return false
		}
		p.typed = next
		if p.cursor > len(p.typed) {
			p.cursor = len(p.typed)
		}
	}
	p.emit(inputComplete{Value: p.typed})
	return true
}

func (p *input) pressBackspace() {
	if len(p.typed) > 0 && p.cursor > 0 {
		p.typed = p.typed[:p.cursor-1] + p.typed[p.cursor:]
		p.cursor--
	}
}

func (p *input) pressLeft() {
	if p.cursor > 0 {
		p.cursor--
	}
}

func (p *input) pressRight() {
	if p.cursor < len(p.typed) {
		p.cursor++
	}
}

func (p *input) pressAny(key rune) {
	p.typed = p.typed[:p.cursor] + string(key) + p.typed[p.cursor:]
	p.cursor++
}

func (p *input) parseTemplates() (err error) {
	tmpl := template.New("dropdown").Funcs(colorFns)
	p.labelTemplate, err = tmpl.New("label").Parse(mustEndWith(p.LabelTemplate, ' '))
	if err != nil {
		return fmt.Errorf("label: %w", err)
	}
	p.answerTemplate, err = tmpl.New("answer").Parse(mustEndWith(p.AnswerTemplate, '\n'))
	if err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	return nil
}
