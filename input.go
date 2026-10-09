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
	"unicode"
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
	// Cursor is the rune index of the cursor after the change. When nil, the
	// cursor is derived from the change in text length.
	Cursor *int
}

type inputConfirmed struct {
	inputIncomingMarker
}

// WithDefault pre-fills the typed text of an input. For a dropdown it is only a
// match hint, not a pre-filled filter: items are ranked by similarity to d, and
// the dropdown returns early only when exactly one item matches d as a prefix.
func WithDefault(d string) opt {
	return func(a any) error {
		switch p := a.(type) {
		case *input:
			p.typed = d
			p.cursor = utf8.RuneCountInString(d)
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
		Answer: i.safeText(i.typed),
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

// run renders the prompt and stops its event goroutine before restoring the terminal.
func (p *input) run() (string, error) {
	io, err := p.makeTermIO(p.in, p.out)
	if err != nil {
		return "", err
	}
	defer io.Restore() //nolint:errcheck
	frame := &bytes.Buffer{}
	// Cancel the event goroutine on return; an outstanding wrapper read is
	// retained for the next prompt on this input (see termIO.startRead).
	runCtx, cancel := context.WithCancel(p.ctx)
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
		done, consumed, err := p.handleNextEvent(io, keys)
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
func (p *input) handleNextEvent(tio *termIO, keys <-chan keyEvent) (done, consumed bool, err error) {
	select {
	case <-p.ctx.Done():
		return false, false, errors.Join(p.ctx.Err(), p.clear(tio))
	case ev, ok := <-keys:
		if !ok && p.ctx.Err() != nil {
			// the reader closed keys because of the cancellation
			return false, false, errors.Join(p.ctx.Err(), p.clear(tio))
		}
		// key press handlers has to be limited to state updates, not writes to the buffer.
		done, err := p.handleKeyEvent(ev, ok)
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

// visibleWindow returns the start and end rune indexes of the portion
// of runes that fits into availW terminal columns, anchored around the cursor.
func (i *input) visibleWindow(runes []rune, availW int) (int, int) {
	total := len(runes)
	cursor := min(max(i.cursor, 0), total)
	if availW <= 0 {
		return cursor, cursor
	}
	if i.cellsWidth(runes) <= availW {
		return 0, total
	}
	half := availW / 2
	start := 0
	switch {
	case i.cellsWidth(runes[:cursor]) <= half:
	case i.cellsWidth(runes[cursor:]) <= half:
		// fill the window backwards from the end of the text
		for w := 0; start < total; start++ {
			w += i.cellWidth(runes[total-1-start])
			if w > availW {
				break
			}
		}
		return i.skipCombining(runes, total-start), total
	default:
		// keep half of the window before the cursor
		start = cursor
		for w := 0; start > 0; start-- {
			w += i.cellWidth(runes[start-1])
			if w > half {
				break
			}
		}
		start = i.skipCombining(runes, start)
	}
	end := start
	for w := 0; end < total; end++ {
		w += i.cellWidth(runes[end])
		if w > availW {
			break
		}
	}
	return start, end
}

// skipCombining moves a clipped window start past zero-width runes, whose
// base rune is outside the window, even past a cursor between them.
func (i *input) skipCombining(runes []rune, start int) int {
	for start > 0 && start < len(runes) && i.cellWidth(runes[start]) == 0 {
		start++
	}
	return start
}

// cellWidth returns the terminal columns of a rune written as valid UTF-8,
// where a replacement character occupies a column.
func (*input) cellWidth(r rune) int {
	if r == utf8.RuneError {
		return 1
	}
	return runeWidth(r)
}

func (i *input) cellsWidth(runes []rune) int {
	w := 0
	for _, r := range runes {
		w += i.cellWidth(r)
	}
	return w
}

// displayRunes returns one terminal-safe rune per typed rune, so that
// the cursor indexes both: a password is masked, and control characters,
// which a paste stores as text, are shown instead of being interpreted.
func (i *input) displayRunes() []rune {
	runes := []rune(i.typed)
	for k, r := range runes {
		if i.Password {
			runes[k] = '*'
		} else {
			runes[k] = i.safeRune(r)
		}
	}
	return runes
}

// safeRune replaces a control character with its Unicode control picture,
// or with the replacement character for C1 controls, which have no picture.
func (*input) safeRune(r rune) rune {
	switch {
	case r < 0x20:
		return 0x2400 + r // e.g. ␍ for a carriage return
	case r == 0x7f:
		return '\u2421' // ␡
	case unicode.IsControl(r):
		return utf8.RuneError
	default:
		return r
	}
}

// safeText applies [input.safeRune] to every rune of text.
func (i *input) safeText(text string) string {
	return strings.Map(i.safeRune, text)
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
	// clip text to a visible window that fits on one row
	runes := i.displayRunes()
	visStart, visEnd := 0, len(runes)
	if io.Width > 0 {
		visStart, visEnd = i.visibleWindow(runes, io.Width-labelW)
	}
	visible := runes[visStart:visEnd]
	visCursor := min(max(i.cursor-visStart, 0), len(visible))
	// write displayed text and clear to the end of the line
	_, err = fmt.Fprintf(frame, "%s\x1b[K", string(visible))
	collect(err)
	moveLeft := i.cellsWidth(visible[visCursor:])
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

func (p *input) handleKeyEvent(ev keyEvent, ok bool) (bool, error) {
	if !ok {
		return false, fmt.Errorf("read: %w", io.EOF)
	}
	_, ok = errors.AsType[*pasteTextError](ev.err)
	if ok {
		// a read started by another widget may report buffered keys as a paste
		return p.handleBufferedKeys(ev.paste)
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
	case '↑', '↓', keyEscape, keyIgnored: // ignore up/down arrows and other special keys
		return false, nil
	}
	return p.applyInputEvent(p.decodeInputEvent(ev.key)), nil
}

// handleBufferedKeys applies keys returned together by one read in order, so
// that Enter or backspace keep their meaning. A paste cannot be told apart from
// them, as there is no paste boundary. Keys after a confirming Enter are dropped.
func (p *input) handleBufferedKeys(buf []byte) (bool, error) {
	decoder := &termIO{in: bytes.NewReader(nil), pending: buf, splitKeys: true}
	for len(decoder.pending) > 0 {
		key, _, err := decoder.readRune()
		done, err := p.handleKeyEvent(keyEvent{key: key, err: err}, true)
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

func (p *input) decodeInputEvent(key rune) inputIncoming {
	switch key {
	case keyEnter:
		return inputConfirmed{}
	case 0x7f: // backspace
		runes := []rune(p.typed)
		cursor := min(p.cursor, len(runes))
		if cursor > 0 {
			runes = append(runes[:cursor-1], runes[cursor:]...)
			cursor--
		}
		return inputChanged{
			Text:   string(runes),
			Cursor: &cursor,
		}
	default:
		return p.insertAtCursor(string(key))
	}
}

// insertAtCursor returns a change that inserts text at the cursor,
// which counts runes rather than bytes, and moves the cursor after it.
func (p *input) insertAtCursor(text string) inputChanged {
	runes := []rune(p.typed)
	inserted := []rune(text)
	cursor := min(p.cursor, len(runes))
	next := cursor + len(inserted)
	return inputChanged{
		Text:   string(runes[:cursor]) + string(inserted) + string(runes[cursor:]),
		Cursor: &next,
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

func (p *input) applyInputChanged(ev inputChanged) {
	prevLen := utf8.RuneCountInString(p.typed)
	prevCursor := p.cursor
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

func (p *input) confirmInput() bool {
	if p.CheckFn != nil {
		next, ok := p.CheckFn(p.typed)
		if !ok {
			return false
		}
		p.typed = next
		p.cursor = min(p.cursor, utf8.RuneCountInString(p.typed))
	}
	p.emit(inputComplete{Value: p.typed})
	return true
}

func (p *input) pressBackspace() {
	if p.cursor > 0 {
		runes := []rune(p.typed)
		p.typed = string(append(runes[:p.cursor-1], runes[p.cursor:]...))
		p.cursor--
	}
}

func (p *input) pressLeft() {
	if p.cursor > 0 {
		p.cursor--
	}
}

func (p *input) pressRight() {
	if p.cursor < utf8.RuneCountInString(p.typed) {
		p.cursor++
	}
}

func (p *input) pressAny(key rune) {
	p.applyInputChanged(p.insertAtCursor(string(key)))
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
