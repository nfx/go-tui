// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
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

// WithDefault supplies the answer of a prompt. Without a terminal the prompt
// returns it without reading input or printing; with a default missing, invalid
// or ambiguous it fails with [ErrInvalidOption], and without any default with
// [ErrNoTTY]. A default of nil, "", 0 or false is still a default.
//
// [Input] and [Password] pre-fill fmt.Sprint(d). [Number] pre-fills d in plain
// decimal notation. Choice widgets keep d as a typed value: the item for
// [Dropdown], the key for [DropdownKV] and [MultichoiceKVK], the value for
// [MultichoiceKVV]; a multichoice also takes a slice of them. Values are
// matched by equality, never by label, and must identify exactly one entry. A
// string that is not an item is a word-prefix hint on the labels.
//
// With a terminal, a dropdown treats a string as a match hint, not a pre-filled
// filter: items are ranked by similarity to d, and the dropdown returns early
// only when exactly one item matches d as a prefix. Multichoice checks a unique
// word-prefix match once and still requires Enter. Lazy choices resolve the
// default after successful exhaustion. [Confirm] takes a bool or "Yes"/"No".
func WithDefault(d any) opt {
	return opT(func(x withDefault) error {
		x.setDefault(d)
		return nil
	})
}

// WithNonEmpty forces the prompt to continue until the confirmed input contains
// at least one non-whitespace character. Empty and whitespace-only input are
// rejected.
func WithNonEmpty() opt {
	return opT(func(i *input) error {
		i.rules = append(i.rules, func(_ context.Context, s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("a value is required")
			}
			return nil
		})
		return nil
	})
}

// WithValidation validates confirmed input in option order; the first error is shown
// below the input. fn receives the prompt context and blocks key handling while it
// runs. Errors must not expose the typed value, especially for [Password].
func WithValidation(fn func(context.Context, string) error) opt {
	return opT(func(i *input) error {
		if fn == nil {
			return fmt.Errorf("%w: validator is nil", ErrInvalidOption)
		}
		i.rules = append(i.rules, fn)
		return nil
	})
}

// WithRegex rejects a confirmed input that is not fully matched by pattern and
// shows message instead. A pattern that matches the empty string accepts it;
// combine with [WithNonEmpty] to require a value.
func WithRegex(pattern, message string) opt {
	return opT(func(i *input) error {
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("%w: regex message is empty", ErrInvalidOption)
		}
		// the non-capturing group keeps alternations like `a|b` from
		// escaping the \A and \z anchors.
		re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
		if err != nil {
			return fmt.Errorf("%w: regex: %w", ErrInvalidOption, err)
		}
		i.rules = append(i.rules, func(_ context.Context, s string) error {
			if !re.MatchString(s) {
				return errors.New(message)
			}
			return nil
		})
		return nil
	})
}

type input struct {
	config

	Label    string
	Hide     bool
	Password bool

	LabelTemplate    string
	labelTemplate    *template.Template
	AnswerTemplate   string
	answerTemplate   *template.Template
	FeedbackTemplate string
	feedbackTmpl     *template.Template
	// labelSet and answerSet record per-call templates, which win over the
	// theme whatever their source.
	labelSet, answerSet bool

	// theme is the snapshot captured at construction. When themed, the prompt
	// renders through it, and run binds it to the destination: look holds the
	// binding, label the label rendered once with it, answer and feedback the
	// bound slots of the prompt family.
	theme            *compiledTheme
	themed           bool
	look             binding
	label            text
	answer, feedback boundSlot
	// numeric selects the Number slots of the theme.
	numeric bool

	def defaultValue
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
func (i *input) setHide() {
	i.Hide = true
}

// implement [withPrompt] interface.
func (i *input) setLabelTemplate(tmpl string) {
	i.LabelTemplate = tmpl
	i.labelSet = true
}

// implement [withPrompt] interface.
func (i *input) setAnswerTemplate(tmpl string) {
	i.AnswerTemplate = tmpl
	i.answerSet = true
}

// implement [withPrompt] interface.
func (i *input) setDefault(d any) {
	i.def = defaultValue{set: true, raw: d}
	i.typed = fmt.Sprint(d)
	i.cursor = utf8.RuneCountInString(i.typed)
}

func newInput(ctx context.Context, label string) *input {
	in, out := defaultStreams(ctx)
	return &input{
		ctx:            ctx,
		out:            out,
		in:             in,
		Label:          label,
		LabelTemplate:  DefaultLabelTemplate,
		AnswerTemplate: DefaultAnswerTemplate,
		theme:          defaultIO.currentTheme(),
		themed:         themeRendering.Load(),
		makeTermIO:     makePromptIO,
	}
}

func Input(ctx context.Context, label string, option ...opt) (string, error) {
	i := newInput(ctx, label)
	return i.read(ctx, option...)
}

func Password(ctx context.Context, label string, option ...opt) (string, error) {
	i := newInput(ctx, label)
	i.Password = true
	return i.read(ctx, option...)
}

func (i *input) read(ctx context.Context, option ...opt) (string, error) {
	err := opts(option).Apply(i)
	if err != nil {
		return "", err
	}
	err = i.parseTemplates()
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
		return "", errNoDefault(i.Label)
	}
	for _, rule := range i.rules {
		err = rule(ctx, i.typed)
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		if err != nil {
			// the text itself stays out of the error, it may be a secret
			return "", errBadDefault("is rejected: %w", err)
		}
	}
	return i.typed, nil
}

func (i *input) showAnswer() error {
	if i.Hide || i.Password {
		return nil
	}
	var buf bytes.Buffer
	var err error
	if i.themed && !i.answerSet {
		err = i.themedAnswer(&buf)
	} else {
		// TODO: unify reused structures
		err = i.answerTemplate.Execute(&buf, dropdownAnswer{
			Label:  i.Label,
			Answer: i.safeText(i.typed),
		})
	}
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
	if p.themed {
		// before the event reader starts, as it may probe the background
		err = p.bind(ctx, io)
		if err != nil {
			return "", err
		}
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

// visibleWindow returns the start and end indexes of the clusters
// that fit into availW terminal columns, anchored around the cursor.
func (i *input) visibleWindow(cells clusters, availW int) (int, int) {
	at := cells.at(i.cursor)
	if availW <= 0 {
		return at, at
	}
	if cells.width() <= availW {
		return 0, len(cells)
	}
	half := availW / 2
	start := 0
	switch {
	case cells[:at].width() <= half:
	case cells[at:].width() <= half:
		// fill the window backwards from the end of the text
		start = len(cells)
		for w := 0; start > 0 && w+cells[start-1].width <= availW; start-- {
			w += cells[start-1].width
		}
	default:
		// keep half of the window before the cursor
		start = at
		for w := 0; start > 0 && w+cells[start-1].width <= half; start-- {
			w += cells[start-1].width
		}
	}
	end := start
	for w := 0; end < len(cells) && w+cells[end].width <= availW; end++ {
		w += cells[end].width
	}
	return start, end
}

// prevBoundary returns the rune offset of the cluster boundary before cursor.
func (i *input) prevBoundary(cursor int) int {
	cells := text(string(i.displayRunes())).clusters()
	if k := cells.at(cursor - 1); k < len(cells) {
		return cells[k].start
	}
	return max(cursor-1, 0)
}

// nextBoundary returns the rune offset of the cluster boundary after cursor.
func (i *input) nextBoundary(cursor int) int {
	cells := text(string(i.displayRunes())).clusters()
	if k := cells.at(cursor); k < len(cells) {
		return cells[k].end
	}
	return cursor + 1
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

// render draws the input and optional feedback, then restores the caret on the input row.
func (i *input) render(io *termIO, frame *bytes.Buffer) error {
	if i.themed {
		return i.renderThemed(io, frame)
	}
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
	var label text
	collect(i.labelTemplate.Execute(&label, i.Label))
	labelW := label.width()
	_, err := frame.Write(label)
	collect(err)
	// clip text to a visible window that fits on one row
	runes := i.displayRunes()
	cells := text(string(runes)).clusters()
	first, last := 0, len(cells)
	if io.Width > 0 {
		first, last = i.visibleWindow(cells, io.Width-labelW)
	}
	visible := runes[cells.runeOffset(first):cells.runeOffset(last)]
	// write displayed text and clear to the end of the line
	_, err = fmt.Fprintf(frame, "%s\x1b[K", string(visible))
	collect(err)
	// a cursor inside of a cluster stands on its first rune
	at := min(max(cells.at(i.cursor), first), last)
	moveLeft := cells[at:last].width()
	feedback, err := i.wrapRejection(io)
	collect(err)
	switch {
	case len(feedback) > 0 || i.rows > 1:
		i.renderFeedback(frame, feedback, collect, cells, labelW, first, at)
	case moveLeft > 0:
		// move the cursor left by the difference between
		// the end and the desired position
		_, err = fmt.Fprintf(frame, "\x1b[%dD", moveLeft)
		collect(err)
	}
	_, err = frame.WriteTo(io)
	collect(err)
	return errors.Join(errs...)
}

func (i *input) wrapRejection(io *termIO) (text, error) {
	var feedback text
	if i.rejection == "" {
		return feedback, nil
	}
	if io.Height > 0 && io.Height < 2 {
		return nil, ErrNoSpace
	}
	// unknown width is unbounded; a known one leaves the last column free
	limit := math.MaxInt
	if io.Width > 0 {
		limit = max(io.Width-1, 1)
	}
	return i.formatFeedback(i.rejection, limit)
}

// renderFeedback row (or erase a stale one) and return to the caret column.
func (i *input) renderFeedback(frame *bytes.Buffer, feedback text,
	collect func(err error), cells clusters, labelW, first, at int) {
	_, err := fmt.Fprintf(frame, "\n\r\x1b[K%s\x1b[1A", feedback)
	collect(err)
	// an absolute column, as a managed viewport drops the carriage return
	_, err = fmt.Fprintf(frame, "\x1b[%dG", labelW+cells[first:at].width()+1)
	collect(err)
	i.rows = 1
	if len(feedback) > 0 {
		i.rows = 2
	}
}

// clear erases the input and any feedback row before the prompt exits.
func (i *input) clear(io *termIO) error {
	var errs []error
	rows := max(i.rows, 1)
	down := rows
	if io.vp == nil {
		// from the caret row to below the frame; a viewport replaces the frame
		down -= i.caretRow
	}
	_, err := fmt.Fprint(io, strings.Repeat("\n", down))
	if err != nil {
		errs = append(errs, fmt.Errorf("newline: %w", err))
	}
	err = io.clear(rows, io)
	if err != nil {
		errs = append(errs, fmt.Errorf("clear: %w", err))
	}
	return errors.Join(errs...)
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

// reject displays non-nil errors.
func (p *input) reject(err error) {
	if err == nil {
		return
	}
	p.rejection = err.Error()
	if p.rejection == "" {
		p.rejection = "Invalid value"
	}
}

// pressLeft moves the cursor to the preceding cluster boundary.
func (p *input) pressLeft() {
	if p.cursor > 0 {
		p.cursor = p.prevBoundary(p.cursor)
	}
}

// pressRight moves the cursor to the next cluster boundary.
func (p *input) pressRight() {
	if p.cursor < utf8.RuneCountInString(p.typed) {
		p.cursor = p.nextBoundary(p.cursor)
	}
}

func (p *input) pressAny(key rune) {
	p.applyInputChanged(p.insertAtCursor(string(key)))
}

// parseTemplates prepares label, answer, and feedback templates before interaction.
// A themed prompt parses only per-call templates, with placeholder helpers
// that run rebinds to the destination; the theme slots are already compiled.
func (p *input) parseTemplates() (err error) {
	if p.theme == nil {
		p.theme = defaultIO.currentTheme()
	}
	funcs := p.theme.legacy
	if p.themed {
		funcs = p.theme.funcs(profilePlain, roleStyles{})
	}
	tmpl := template.New("dropdown").Funcs(funcs)
	if !p.themed || p.labelSet {
		p.labelTemplate, err = tmpl.New("label").Parse(mustEndWith(p.LabelTemplate, ' '))
		if err != nil {
			return fmt.Errorf("label: %w", err)
		}
	}
	if !p.themed || p.answerSet {
		p.answerTemplate, err = tmpl.New("answer").Parse(mustEndWith(p.AnswerTemplate, '\n'))
		if err != nil {
			return fmt.Errorf("answer: %w", err)
		}
	}
	return p.parseFeedback(funcs)
}

// DefaultFeedbackTemplate renders the message shown below an input whose answer
// was rejected. It receives the sanitized .Message.
var DefaultFeedbackTemplate = `{{ red (bold "Error: ") .Message }}`

// WithFeedbackTemplate overrides the one-row message shown below an [Input] or
// [Password] for a rejected answer. An empty template restores the default.
// The template sees only .Message.
func WithFeedbackTemplate(tmpl string) opt {
	return opT(func(i *input) error {
		i.FeedbackTemplate = tmpl
		return nil
	})
}

// parseFeedback prepares the feedback template, using the default for an
// empty override. A themed prompt uses its feedback slot instead.
func (p *input) parseFeedback(funcs template.FuncMap) error {
	source := p.FeedbackTemplate
	if source == "" && p.themed {
		p.feedbackTmpl = nil
		return nil
	}
	if source == "" {
		source = DefaultFeedbackTemplate
	}
	parsed, err := template.New("feedback").Funcs(funcs).Parse(source)
	if err != nil {
		return fmt.Errorf("feedback: %w", err)
	}
	p.feedbackTmpl = parsed
	return nil
}

// feedbackSafe drops line breaks and terminal controls, so that a message
// cannot move the cursor or start an escape sequence.
func (p *input) feedbackSafe(message string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(message, "�"))
}

// formatFeedback renders one row that fits into width columns.
func (p *input) formatFeedback(message string, width int) (text, error) {
	var buf text
	err := p.feedbackTmpl.Execute(&buf, feedbackView{p.feedbackSafe(message)})
	if err != nil {
		return nil, fmt.Errorf("feedback: %w", err)
	}
	return buf.beforeNewline().truncateColumns(width), nil
}

// labelView is the payload of a label slot.
type labelView struct{ Label string }

// answerView is the payload of an answer slot: Value is the formatted answer,
// Values and Count the answer as a collection, for scalar prompts one value.
type answerView struct {
	Label  string
	Value  string
	Values []string
	Count  int
}

// feedbackView is the payload of a feedback slot and of per-call feedback
// templates: the sanitized rejection message.
type feedbackView struct{ Message string }

// slots selects the theme slots of the prompt family. A password retains no answer.
func (p *input) slots() (label, answer, feedback *compiledSlot) {
	t := &p.theme.templates
	switch {
	case p.Password:
		return t.Password.Label, nil, t.Password.Feedback
	case p.numeric:
		return t.Number.Label, t.Input.Answer, t.Number.Feedback
	}
	return t.Input.Label, t.Input.Answer, t.Input.Feedback
}

// bind captures the presentation of one run for its destination, before the
// first frame: the helpers, per-call templates rebound to them, the bound
// slots, and the label, which renders once. tt is the terminal lease.
func (p *input) bind(ctx context.Context, tt *termIO) error {
	p.look = defaultIO.bindTheme(ctx, p.theme, p.out, tt)
	for _, t := range []**template.Template{&p.labelTemplate, &p.answerTemplate, &p.feedbackTmpl} {
		if *t == nil {
			continue
		}
		clone, err := (*t).Clone()
		if err != nil {
			return fmt.Errorf("%s: %w", (*t).Name(), err)
		}
		*t = clone.Funcs(p.look.funcs)
	}
	label, answer, feedback := p.slots()
	var err error
	if p.answer, err = answer.bind(p.look.funcs); err != nil {
		return err
	}
	if p.feedback, err = feedback.bind(p.look.funcs); err != nil {
		return err
	}
	if p.labelSet {
		p.label = nil
		if err = p.labelTemplate.Execute(&p.label, p.Label); err != nil {
			return fmt.Errorf("label: %w", err)
		}
		return nil
	}
	slot, err := label.bind(p.look.funcs)
	if err != nil {
		return err
	}
	p.label, err = slot.render(labelView{p.Label})
	if err != nil {
		return err
	}
	if p.theme.source.Inline && p.label.width() > 0 {
		// the renderer owns the spacing before the value
		p.label = append(p.label, ' ')
	}
	return nil
}

// renderThemed draws the frame in the geometry of the theme: in Guided, the
// label row, the value row behind the guide and the feedback row behind it;
// in Inline, the label leads the value row. The caret ends on the value row,
// and the next frame starts from there.
func (i *input) renderThemed(io *termIO, frame *bytes.Buffer) error {
	io.refreshSize()
	frame.Reset()
	g := i.look.geometry(io.Width, io.Height)
	lead := g.valueLead(i.label)
	cols, err := g.valueColumns(lead)
	if err != nil {
		return err
	}
	runes := i.displayRunes()
	cells := text(string(runes)).clusters()
	first, last := i.visibleWindow(cells, cols)
	visible := text(string(runes[cells.runeOffset(first):cells.runeOffset(last)]))
	feedback, err := i.themedFeedback()
	if err != nil {
		return err
	}
	rows := make([]frameRow, 0, 3)
	if !g.inline {
		rows = append(rows, frameRow{content: i.label, drop: 1})
	}
	value := len(rows)
	row := append(append(text{}, lead...), i.look.style(i.look.roles.foreground, visible)...)
	rows = append(rows, frameRow{content: row})
	if len(feedback) > 0 {
		rows = append(rows, frameRow{content: feedback, guided: true})
	}
	fit, err := g.fit(rows)
	if err != nil {
		return err
	}
	// a cursor inside of a cluster stands on its first rune
	at := min(max(cells.at(i.cursor), first), last)
	i.writeFrame(io, frame, fit, fit.at[value], lead.width()+cells[first:at].width(), cells[at:last].width())
	_, err = frame.WriteTo(io)
	return err
}

// writeFrame draws fitted rows from the top of the previous frame, erases
// the rows it had beyond them and puts the caret on row caret, col columns
// in, which is left columns before the end of that row.
func (i *input) writeFrame(io *termIO, frame *bytes.Buffer, fit fitted, caret, col, left int) {
	if io.vp == nil && i.caretRow > 0 {
		fmt.Fprintf(frame, "\x1b[%dA", i.caretRow)
	}
	for k, line := range fit.lines {
		if k > 0 {
			frame.WriteByte('\n')
		}
		frame.WriteByte('\r')
		frame.Write(line)
		frame.WriteString("\x1b[K")
	}
	drawn, stale := len(fit.lines), i.rows
	if io.Height > 0 {
		// rows past the bottom of a shorter terminal are gone, and a newline
		// there would scroll away the rows just drawn
		stale = min(stale, max(io.Height, drawn))
	}
	for ; drawn < stale; drawn++ {
		// erase a row of the taller previous frame
		frame.WriteString("\n\r\x1b[K")
	}
	switch up := drawn - 1 - caret; {
	case up > 0:
		// an absolute column, as a managed viewport drops the carriage return
		fmt.Fprintf(frame, "\x1b[%dA\x1b[%dG", up, col+1)
	case left > 0:
		fmt.Fprintf(frame, "\x1b[%dD", left)
	}
	i.rows, i.caretRow = len(fit.lines), caret
}

// themedFeedback renders the rejection with the per-call template, cut at its
// first line as before, or with the feedback slot. The geometry clips it.
func (i *input) themedFeedback() (text, error) {
	switch {
	case i.rejection == "":
		return nil, nil
	case i.feedbackTmpl != nil:
		return i.formatFeedback(i.rejection, math.MaxInt)
	}
	return i.feedback.render(feedbackView{i.feedbackSafe(i.rejection)})
}

// themedAnswer renders the answer slot of the family and ends its line. A
// hidden or empty answer retains nothing.
func (i *input) themedAnswer(buf *bytes.Buffer) error {
	value := i.safeText(i.typed)
	out, err := i.answer.render(answerView{Label: i.Label, Value: value, Values: []string{value}, Count: 1})
	if err != nil || out.width() == 0 {
		return err
	}
	buf.Write(out)
	return buf.WriteByte('\n')
}
