// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

// Multichoice returns checked items in input order. Enter confirms without
// checking the focused item; a successful empty selection is a non-nil slice.
// Items are shallowly copied. Use one active prompt per shared input reader.
func Multichoice[T any](ctx context.Context, label string, items []T, o ...opt) ([]T, error) {
	if len(items) == 0 {
		return nil, ErrNoItems
	}
	snapshot := slices.Clone(items)
	m := newMultichoice(ctx)
	m.Label = label
	for _, item := range snapshot {
		m.Items = append(m.Items, item)
	}
	err := m.run(ctx, o...)
	if err != nil {
		return nil, err
	}
	result := make([]T, 0, m.count)
	for _, i := range m.indices() {
		result = append(result, snapshot[i])
	}
	return result, nil
}

// MultichoiceKVK snapshots entries sorted by fmt.Sprint(key) and returns keys.
// Equal formatted keys have unspecified relative order. Templates receive
// entries with Key and Value, including every element of the answer collection.
func MultichoiceKVK[K comparable, V any](ctx context.Context, label string, items map[K]V, o ...opt) ([]K, error) {
	entries, err := multichoiceMap(ctx, label, items, o)
	if err != nil {
		return nil, err
	}
	result := make([]K, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Key)
	}
	return result, nil
}

// MultichoiceKVV returns values in the same entry order as MultichoiceKVK.
// Duplicate and non-comparable values remain independently selectable.
func MultichoiceKVV[K comparable, V any](ctx context.Context, label string, items map[K]V, o ...opt) ([]V, error) {
	entries, err := multichoiceMap(ctx, label, items, o)
	if err != nil {
		return nil, err
	}
	result := make([]V, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Value)
	}
	return result, nil
}

// multichoiceMap snapshots key/value pairs in display order before sharing selection logic.
func multichoiceMap[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	o []opt,
) ([]mapKV[K, V], error) {
	entries := make([]mapKV[K, V], 0, len(items))
	for key, value := range items {
		entries = append(entries, mapKV[K, V]{key, value})
	}
	sort.SliceStable(entries, func(i, j int) bool { return fmt.Sprint(entries[i].Key) < fmt.Sprint(entries[j].Key) })
	defaults := []opt{WithTemplate(".Key")}
	return Multichoice(ctx, label, entries, append(defaults, o...)...)
}

// MultichoiceLazy displays a finite sequence progressively and enables Enter
// only after successful exhaustion. Results keep producer arrival order.
// Publication stops on cancellation; iterator work blocked outside yield must
// observe a context supplied by its caller to be interruptible.
func MultichoiceLazy[T any](ctx context.Context, label string, items iter.Seq2[T, error], o ...opt) ([]T, error) {
	if items == nil {
		return nil, fmt.Errorf("%w: MultichoiceLazy sequence is nil", ErrInvalidOption)
	}
	m := newMultichoice(ctx)
	m.Label = label
	m.loading = true
	m.producer = func(yield func(any, error) bool) {
		for item, err := range items {
			if !yield(item, err) {
				return
			}
		}
	}
	err := m.run(ctx, o...)
	if err != nil {
		return nil, err
	}
	result := make([]T, 0, m.count)
	for _, i := range m.indices() {
		// The producer preserves T, including nil interface values.
		value, ok := m.Items[i].(T)
		if !ok && m.Items[i] != nil {
			return nil, fmt.Errorf("%w: item %d is not the requested type", ErrInvalidOption, i)
		}
		result = append(result, value)
	}
	return result, nil
}

// ErrInvalidOption identifies invalid configuration values.
var ErrInvalidOption = errors.New("invalid option")

// DefaultMultichoiceItemTemplate decorates item content with focus and check state.
var DefaultMultichoiceItemTemplate = `{{if .Active}}→{{else}} {{end}} [{{if .Selected}}x{{else}} {{end}}] {{.Value}}`

// DefaultMultichoiceAnswerTemplate lists selected labels, or explicitly says none.
var DefaultMultichoiceAnswerTemplate = `{{ dim "✔ " .Label " …" }} ` +
	`{{range $i, $item := .Answer}}{{if $i}}, {{end}}{{bold (label $item)}}{{else}}none{{end}}`

type multichoice struct {
	Ctx   context.Context
	Label string
	Items []any

	LabelTemplate        string
	ActiveItemTemplate   string
	InactiveItemTemplate string
	MoreItemsTemplate    string
	AnswerTemplate       string
	answerSet            bool // an explicit WithAnswerTemplate wins over generated ones
	ItemTemplate         string

	labelBuf             text
	itemTemplate         *template.Template
	activeItemTemplate   *template.Template
	inactiveItemTemplate *template.Template
	moreItemsTemplate    *template.Template
	answerTemplate       *template.Template
	trie                 *trie
	inactive, contents   []text
	hintRunes            []rune
	scores               []int

	// Source indexes stay private; visibility never owns checked state.
	relevant    []int
	displayed   []int
	selected    []bool
	toggled     []bool
	active      int
	offset      int
	count       int
	hint        string
	typed       string
	Hide        bool
	loading     bool
	manualFocus bool

	// Reject Enter decoded during loading even if exhaustion is handled first.
	readyOrder uint64
	producer   iter.Seq2[any, error]
	in         io.Reader
	out        io.Writer
	makeTermIO func(io.Reader, io.Writer) (*termIO, error)
}

// newMultichoice installs defaults without acquiring terminal resources.
func newMultichoice(ctx context.Context) *multichoice {
	return &multichoice{
		Ctx:                  ctx,
		Label:                "Select items",
		makeTermIO:           makePromptIO,
		LabelTemplate:        DefaultLabelTemplate,
		ActiveItemTemplate:   `{{label .}}`,
		InactiveItemTemplate: `{{label .}}`,
		ItemTemplate:         DefaultMultichoiceItemTemplate,
		MoreItemsTemplate:    DefaultMoreItemsTemplate,
		AnswerTemplate:       DefaultMultichoiceAnswerTemplate,
	}
}

// setReader supplies the input used when the prompt acquires its terminal.
func (m *multichoice) setReader(r io.Reader) { m.in = r }

// setWriter unwraps Tui output so prompts share its managed renderer.
func (m *multichoice) setWriter(w io.Writer) {
	if t, ok := w.(*Tui); ok {
		w = t.out
	}
	m.out = w
}

// setHide suppresses the final answer while retaining the interactive prompt.
func (m *multichoice) setHide() { m.Hide = true }

// setLabelTemplate configures the heading independently of item content.
func (m *multichoice) setLabelTemplate(t string) { m.LabelTemplate = t }

// setAnswerTemplate configures the confirmed collection summary.
func (m *multichoice) setAnswerTemplate(t string) { m.AnswerTemplate, m.answerSet = t, true }

// setDefault records a selection hint without pre-filling the filter.
func (m *multichoice) setDefault(hint string) { m.hint = hint }

// setActiveItemTemplate configures focused content independently of the checkbox wrapper.
func (m *multichoice) setActiveItemTemplate(t string) { m.ActiveItemTemplate = t }

// setInactiveItemTemplate configures unfocused content independently of the checkbox wrapper.
func (m *multichoice) setInactiveItemTemplate(t string) { m.InactiveItemTemplate = t }

// setMoreItemsTemplate configures the footer for choices outside the visible window.
func (m *multichoice) setMoreItemsTemplate(t string) { m.MoreItemsTemplate = t }

// setContentTemplate shares item content with collection answers, keeping details active-only.
func (m *multichoice) setContentTemplate(main string, details ...string) {
	extra := ""
	if len(details) > 0 {
		extra = fmt.Sprintf(` {{dim "(" (%s) ")"}}`, strings.Join(details, `) ", " (`))
	}
	m.ActiveItemTemplate = fmt.Sprintf(`{{cyan %s}}%s`, main, extra)
	m.InactiveItemTemplate = fmt.Sprintf(`{{%s}}`, main)
	if !m.answerSet {
		m.AnswerTemplate = fmt.Sprintf(
			`{{dim "✔ " .Label " …"}} {{range $i, $item := .Answer}}{{if $i}}, {{end}}{{bold %s}}{{else}}none{{end}}`,
			main,
		)
	}
}

// setFieldTemplate keeps named fields consistent across rows and collection answers.
func (m *multichoice) setFieldTemplate(fields ...string) {
	expr := make([]string, len(fields))
	for i, f := range fields {
		expr[i] = "." + f
	}
	main := strings.Join(expr, ` ", " `)
	m.ActiveItemTemplate = fmt.Sprintf(`{{cyan %s}}`, main)
	m.InactiveItemTemplate = fmt.Sprintf(`{{print %s}}`, main)
	if !m.answerSet {
		m.AnswerTemplate = fmt.Sprintf(
			`{{dim "✔ " .Label " …"}} {{range $i, $item := .Answer}}{{if $i}}, {{end}}{{bold %s}}{{else}}none{{end}}`,
			main,
		)
	}
}

// prepare validates options and builds item caches before acquiring terminal resources.
func (m *multichoice) prepare(o []opt) error {
	err := m.Ctx.Err()
	if err != nil {
		return err
	}
	err = opts(o).Apply(m)
	if err != nil {
		return err
	}
	if !m.loading && len(m.Items) == 0 {
		return ErrNoItems
	}
	err = m.parseTemplates()
	if err != nil {
		return fmt.Errorf("templates: %w", err)
	}
	m.trie = newTrie()
	m.hintRunes = []rune(strings.ToLower(m.hint))
	items := m.Items
	m.Items = nil
	m.selected = nil
	m.toggled = nil
	for _, item := range items {
		if err := m.addItem(item); err != nil {
			return err
		}
	}
	if !m.loading {
		m.resolveDefault()
	}
	return m.Ctx.Err()
}

// parseTemplates checks template syntax and caches the label before interaction.
func (m *multichoice) parseTemplates() error {
	root := template.New("multichoice").Funcs(choiceTemplateFuncs())
	for _, part := range []struct {
		name, source string
		dest         **template.Template
	}{
		{"row", m.ItemTemplate, &m.itemTemplate}, {"active", m.ActiveItemTemplate, &m.activeItemTemplate},
		{"inactive", m.InactiveItemTemplate, &m.inactiveItemTemplate}, {"more", m.MoreItemsTemplate, &m.moreItemsTemplate},
		{"answer", mustEndWith(m.AnswerTemplate, '\n'), &m.answerTemplate},
	} {
		parsed, err := root.New(part.name).Parse(part.source)
		if err != nil {
			return fmt.Errorf("%s: %w", part.name, err)
		}
		*part.dest = parsed
	}
	label, err := root.New("label").Parse(m.LabelTemplate)
	if err != nil {
		return fmt.Errorf("label: %w", err)
	}
	m.labelBuf = nil
	if err := label.Execute(&m.labelBuf, m.Label); err != nil {
		return fmt.Errorf("label: %w", err)
	}
	return nil
}

// addItem renders and indexes each arrival once; subsequent frames use caches.
func (m *multichoice) addItem(item any) error {
	err := m.Ctx.Err()
	if err != nil {
		return err
	}
	var active, inactive text
	err = m.activeItemTemplate.Execute(&active, item)
	if err != nil {
		return fmt.Errorf("active item: %w", err)
	}
	err = m.inactiveItemTemplate.Execute(&inactive, item)
	if err != nil {
		return fmt.Errorf("inactive item: %w", err)
	}
	i := len(m.Items)
	m.Items = append(m.Items, item)
	m.contents = append(m.contents, active)
	m.inactive = append(m.inactive, inactive)
	m.selected = append(m.selected, false)
	m.toggled = append(m.toggled, false)
	m.trie.Add(inactive, i)
	score := 0
	if m.hint != "" {
		score = choiceScore(m.choicePlainText(inactive), m.hintRunes)
	}
	m.scores = append(m.scores, score)
	matches := true
	if m.typed != "" {
		single := newTrie()
		single.Add(inactive, i)
		matches = len(single.Prefix(m.typed)) > 0
	}
	if matches {
		focused := m.focused()
		pos := sort.Search(len(m.relevant), func(j int) bool { return m.less(i, m.relevant[j]) })
		m.relevant = slices.Insert(m.relevant, pos, i)
		m.offset = 0
		m.active = 0
		if m.manualFocus {
			m.setFocus(focused)
		}
	}
	return nil
}

// choicePlainText removes terminal styling and preserves word boundaries for ranking.
func (m *multichoice) choicePlainText(t text) string {
	var b strings.Builder
	for seg := range t.segments() {
		if seg.isText() {
			b.Write(seg.text)
		} else if bytes.Equal(seg.text, []byte{'\n'}) || bytes.Equal(seg.text, []byte{'\t'}) {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// less ranks by the default hint, using arrival order to break ties.
func (m *multichoice) less(a, b int) bool {
	if m.scores[a] == m.scores[b] {
		return a < b
	}
	return m.scores[a] < m.scores[b]
}

// focused returns the source item identity, or -1 when no match is focused.
func (m *multichoice) focused() int {
	pos := m.offset + m.active
	if pos < 0 || pos >= len(m.relevant) {
		return -1
	}
	return m.relevant[pos]
}

// setFocus preserves item identity when its position in the filtered list changes.
func (m *multichoice) setFocus(i int) {
	if pos := slices.Index(m.relevant, i); pos >= 0 {
		m.active = pos
		m.offset = 0
	}
}

// resolveDefault checks a unique match without overriding manual toggles or focus.
func (m *multichoice) resolveDefault() {
	if m.hint == "" {
		return
	}
	matches := m.trie.Prefix(m.hint)
	if len(matches) != 1 {
		return
	}
	i := matches[0]
	if !m.selected[i] && !m.toggled[i] {
		m.selected[i] = true
		m.count++
	}
	if !m.manualFocus {
		m.setFocus(i)
	}
}

// filter preserves checked items and retains focus when that item still matches.
func (m *multichoice) filter(query string) {
	focused := m.focused()
	m.typed = query
	m.manualFocus = true
	m.relevant = m.trie.Prefix(query)
	sort.SliceStable(m.relevant, func(i, j int) bool { return m.less(m.relevant[i], m.relevant[j]) })
	m.offset = 0
	m.active = 0
	m.setFocus(focused)
}

// indices returns checked identities in input order, independent of filtering and ranking.
func (m *multichoice) indices() []int {
	result := make([]int, 0, m.count)
	for i, checked := range m.selected {
		if checked {
			result = append(result, i)
		}
	}
	return result
}

// clampRows keeps the focused item visible as the available row count changes.
func (m *multichoice) clampRows(capacity int) {
	if len(m.relevant) == 0 {
		m.displayed = nil
		m.offset = 0
		m.active = 0
		return
	}
	capacity = min(capacity, len(m.relevant))
	pos := min(max(0, m.offset+m.active), len(m.relevant)-1)
	m.offset = min(m.offset, pos, len(m.relevant)-capacity)
	m.offset = max(m.offset, pos-capacity+1)
	m.displayed = m.relevant[m.offset : m.offset+capacity]
	m.active = pos - m.offset
}

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
		clusters := text(m.typed).clusters()
		if len(clusters) > 0 {
			runes := []rune(m.typed)
			m.filter(string(runes[:clusters[len(clusters)-1].start]))
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

// choiceLine uses the shared cluster/ANSI truncator and shows omitted content.
func (m *multichoice) choiceLine(content text, width int) text {
	content = text(strings.TrimRight(string(content), "\r\n"))
	first := content.beforeNewline()
	if len(first) < len(content) {
		first = append(slices.Clone(first), []byte("…")...)
	}
	return first.truncateColumns(width)
}

// render builds a width-bounded frame from cached content and current selection state.
func (m *multichoice) render(tio *termIO, buf *bytes.Buffer) error {
	tio.refreshSize()
	if tio.Width < 1 || tio.Height < 3 {
		return ErrNoSpace
	}
	capacity := tio.Height / 2
	footer := len(m.relevant) > capacity
	m.clampRows(capacity)
	writeLine := func(content text) {
		buf.WriteByte('\r')
		buf.Write(m.choiceLine(content, tio.Width))
		buf.WriteByte('\n')
	}
	writeLine(m.labelBuf)
	if len(m.displayed) == 0 && (!m.loading || len(m.Items) > 0 || m.typed != "") {
		writeLine(text("No matches"))
	}
	for row, i := range m.displayed {
		content := m.inactive[i]
		if row == m.active {
			content = m.contents[i]
		}
		var decorated text
		if err := m.itemTemplate.Execute(&decorated, struct {
			Value            string
			Selected, Active bool
			Index            int
		}{string(content), m.selected[i], row == m.active, i}); err != nil {
			return fmt.Errorf("row: %w", err)
		}
		writeLine(decorated)
	}
	if footer {
		var more text
		if err := m.moreItemsTemplate.Execute(
			&more,
			dropdownMore{More: len(m.relevant) - m.offset - len(m.displayed), Total: len(m.relevant)},
		); err != nil {
			return fmt.Errorf("more: %w", err)
		}
		writeLine(more)
	}
	buf.WriteByte('\r')
	return nil
}

// showAnswer emits the confirmed collection in input order and reports managed output failures.
func (m *multichoice) showAnswer() error {
	if m.Hide {
		return nil
	}
	answer := make([]any, 0, m.count)
	for _, i := range m.indices() {
		answer = append(answer, m.Items[i])
	}
	var buf bytes.Buffer
	if err := m.answerTemplate.Execute(&buf, struct {
		Label  string
		Answer []any
		Count  int
	}{m.Label, answer, len(answer)}); err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	if _, err := buf.WriteTo(m.out); err != nil {
		return fmt.Errorf("answer write: %w", err)
	}
	if cio, ok := m.out.(*chanIO); ok {
		if err := cio.Flush(); err != nil {
			return fmt.Errorf("answer write: %w", err)
		}
	}
	return nil
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
	m.resolveDefault()
	m.readyOrder = nextInputOrder()
	return nil
}

// writeMultichoiceFrame waits for managed output to reach the terminal, so
// asynchronous writer failures are reported by the prompt that produced it.
func (m *multichoice) writeMultichoiceFrame(tio *termIO, frame *bytes.Buffer) error {
	if tio.vp != nil && tio.cio != nil {
		if err := tio.vp.writeAndWait(frame.Bytes(), tio.Width); err != nil {
			return err
		}
		return tio.cio.Flush()
	}
	_, err := frame.WriteTo(tio)
	return err
}

// clearMultichoiceFrame removes the prompt and waits for managed cleanup to reach the terminal.
func (m *multichoice) clearMultichoiceFrame(tio *termIO, space int) error {
	if tio.vp != nil {
		if tio.cio != nil {
			if err := tio.vp.writeAndWait(nil); err != nil {
				return err
			}
			return tio.cio.Flush()
		}
		_, err := tio.vp.Write(nil)
		return err
	}
	if space == 0 {
		return nil
	}
	var frame bytes.Buffer
	if err := tio.clear(space, &frame); err != nil {
		return err
	}
	_, err := frame.WriteTo(tio)
	return err
}

// run emits an answer only after successful interaction and cleanup.
func (m *multichoice) run(ctx context.Context, o ...opt) error {
	if err := m.prepare(o); err != nil {
		return err
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
