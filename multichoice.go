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
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Multichoice returns checked items in input order. Enter confirms without
// checking the focused item; a successful empty selection is a non-nil slice.
// Items are shallowly copied. Use one active prompt per shared input reader.
func Multichoice[T any](ctx context.Context, label string, items []T, o ...opt) ([]T, error) {
	return pickMultichoice(ctx, label, items, targetOf[T](), o)
}

// pickMultichoice runs a multichoice whose typed defaults have the type of target.
func pickMultichoice[T any](ctx context.Context, label string, items []T, target choiceTarget, o []opt) ([]T, error) {
	if len(items) == 0 {
		return nil, ErrNoItems
	}
	snapshot := slices.Clone(items)
	m := newMultichoice(ctx)
	m.target = target
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
	target := choiceTarget{
		typ:  reflect.TypeFor[K](),
		proj: mapKey[K, V],
	}
	entries, err := multichoiceMap(ctx, label, items, target, o)
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
	target := choiceTarget{
		typ:  reflect.TypeFor[V](),
		proj: mapValue[K, V],
	}
	entries, err := multichoiceMap(ctx, label, items, target, o)
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
	target choiceTarget,
	o []opt,
) ([]mapKV[K, V], error) {
	entries := make([]mapKV[K, V], 0, len(items))
	for key, value := range items {
		entries = append(entries, mapKV[K, V]{key, value})
	}
	sort.SliceStable(entries, func(i, j int) bool { return fmt.Sprint(entries[i].Key) < fmt.Sprint(entries[j].Key) })
	// the theme presents the entries, projected to their keys
	return pickMultichoice(ctx, label, entries, target, append([]opt{WithTemplate("(label .Key)")}, o...))
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
	m.target = targetOf[T]()
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

type multichoice struct {
	Ctx   context.Context
	Label string
	Items []any

	projection

	// theme is the snapshot captured at construction. The multichoice
	// renders through it: look is its binding to the destination,
	// slot the bound slots, label the row rendered once per run and help
	// the bound hint rendered every frame with the count of hidden items.
	theme *compiledTheme
	look  binding
	slot  multichoiceSlots
	label text
	help  boundSlot
	// pending holds the items until binding projects them.
	pending []any
	// content is the projected main content of every item. Its details
	// render when a frame first draws it, which drawn records.
	content, details []text
	drawn            []bool

	trie      *trie
	hintRunes []rune
	scores    []int

	// Source indexes stay private; visibility never owns checked state.
	relevant    []int
	displayed   []int
	selected    []bool
	toggled     []bool
	active      int
	offset      int
	count       int
	hint        string
	def         defaultValue
	target      choiceTarget
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
		Ctx:        ctx,
		Label:      "Select items",
		makeTermIO: makePromptIO,
		theme:      defaultIO.currentTheme(),
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

// setDefault records the default; a string is also a selection hint that
// does not pre-fill the filter.
func (m *multichoice) setDefault(d any) {
	m.def = defaultValue{set: true, raw: d}
	m.hint, _ = d.(string) //nolint:errcheck // not a string is no hint
}

// setContentTemplate shares item content with collection answers, keeping details active-only.
func (m *multichoice) setContentTemplate(main string, details ...string) {
	m.setProjection(main, details...)
}

// prepare validates options and defers item projection until destination binding.
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
	m.pending = items // projected once bound to the destination
	return m.Ctx.Err()
}

// parseTemplates checks the syntax of the projection.
func (m *multichoice) parseTemplates() error {
	return m.parse(m.theme, "multichoice")
}

// addItem renders and indexes each arrival once; subsequent frames use caches.
func (m *multichoice) addItem(item any) error {
	err := m.Ctx.Err()
	if err != nil {
		return err
	}
	i := len(m.Items)
	search, err := m.indexItem(item)
	if err != nil {
		return err
	}
	m.Items = append(m.Items, item)
	m.selected = append(m.selected, false)
	m.toggled = append(m.toggled, false)
	m.trie.Add(search, i)
	score := 0
	if m.hint != "" {
		score = choiceScore(choiceLabels{}.plainText(search), m.hintRunes)
	}
	m.scores = append(m.scores, score)
	matches := true
	if m.typed != "" {
		single := newTrie()
		single.Add(search, i)
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

// indexItem caches item content and returns the text used for filtering.
func (m *multichoice) indexItem(item any) (text, error) {
	return m.projectItem(item)
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
// A typed default checks every entry it identifies, and must identify them.
func (m *multichoice) resolveDefault() error {
	var picked []int
	switch {
	case m.def.set && m.hint == "":
		if _, isHint := m.def.raw.(string); !isHint {
			var err error
			picked, err = m.resolveTyped()
			if err != nil {
				return err
			}
		}
	case m.hint != "":
		matches := m.trie.Prefix(m.hint)
		if len(matches) == 1 {
			picked = matches
		}
	}
	for _, i := range picked {
		if !m.selected[i] && !m.toggled[i] {
			m.selected[i] = true
			m.count++
		}
	}
	if len(picked) > 0 && !m.manualFocus {
		m.setFocus(picked[0])
	}
	return nil
}

// resolveTyped resolves the default against the entries collected so far.
func (m *multichoice) resolveTyped() ([]int, error) {
	return m.target.resolveSelection(m.def.raw, m.Items, hintFromTrie(m.trie))
}

// answerFromDefault checks the entries of the default without a terminal.
// It reads no input and prints nothing. A lazy producer starts only when there
// is a default, and is consumed to the end before the default is resolved.
func (m *multichoice) answerFromDefault(ctx context.Context) error {
	err := ctx.Err()
	if err != nil {
		return err
	}
	if !m.def.set {
		return errNoDefault(m.Label)
	}
	// without a terminal only the projection runs, for the hint to match
	err = m.bind(defaultIO.bindTheme(ctx, m.theme, m.out, nil))
	if err != nil {
		return err
	}
	if m.loading {
		// the channel lets cancellation return while the iterator is blocked
		pctx, cancel := context.WithCancel(ctx)
		defer cancel()
		ch := m.startProducer(pctx)
	drain:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case pair, more := <-ch:
				if !more {
					break drain
				}
				if pair.err != nil {
					return fmt.Errorf("producer: %w", pair.err)
				}
				err = m.addItem(pair.item)
				if err != nil {
					return err
				}
			}
		}
		m.loading = false
		if len(m.Items) == 0 {
			return ErrEmptyLazyResult
		}
	}
	picked, err := m.resolveTyped()
	if err != nil {
		return err
	}
	for _, i := range picked {
		m.selected[i] = true
		m.count++
	}
	return ctx.Err()
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

// render builds a width-bounded frame from cached content and current selection state.
func (m *multichoice) render(tio *termIO, buf *bytes.Buffer) error {
	return m.renderThemed(tio, buf)
}

// noMatches distinguishes an empty match from a sequence still awaiting its first item.
func (m *multichoice) noMatches() bool {
	return len(m.displayed) == 0 && (!m.loading || len(m.Items) > 0 || m.typed != "")
}

// multichoiceRow is the payload of the row template and of the Row slot:
// Value is the content of the item in its state, which the row decorates
// with the focus and check marks.
type multichoiceRow struct {
	Value            string
	Selected, Active bool
	Index            int
}

// showAnswer emits the confirmed collection in input order and reports managed output failures.
func (m *multichoice) showAnswer() error {
	if m.Hide {
		return nil
	}
	return m.themedAnswer()
}

// writeAnswer writes the answer and waits for managed output to acknowledge it.
func (m *multichoice) writeAnswer(line []byte) error {
	n, err := m.out.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("answer write: %w", err)
	}
	if cio, ok := m.out.(*chanIO); ok {
		if err := cio.Flush(); err != nil {
			return fmt.Errorf("answer write: %w", err)
		}
	}
	return nil
}

// multichoiceSlots are the theme slots of a multichoice bound to one destination.
type multichoiceSlots struct {
	choiceSlots
	row, answerValue, empty boundSlot
}

// multichoiceKeys are the bindings beyond confirming that the hint row describes.
func (m *multichoice) multichoiceKeys(s Symbols) []hint {
	return []hint{{s.arrows(), text("move")}, {text("space"), text("toggle")}, {text("type"), text("filter")}}
}

// emptyView is the payload of the Empty slot: the filter that matches nothing.
type emptyView struct{ Query string }

// collectionView is the payload of the AnswerValue slot: the projected
// content of the checked items in input order.
type collectionView struct {
	Values []string
	Count  int
}

// bind installs destination-bound templates and projects pending items.
func (m *multichoice) bind(look binding) error {
	m.look = look
	err := m.rebind(look.funcs)
	if err != nil {
		return err
	}
	s := m.theme.templates.Multichoice
	m.slot.choiceSlots, err = s.bind(look.funcs)
	if err != nil {
		return err
	}
	var errs [3]error
	m.slot.row, errs[0] = s.Row.bind(look.funcs)
	m.slot.answerValue, errs[1] = s.AnswerValue.bind(look.funcs)
	m.slot.empty, errs[2] = s.Empty.bind(look.funcs)
	if err := errors.Join(errs[:]...); err != nil {
		return err
	}
	items := m.pending
	m.pending = nil
	for _, item := range items {
		if err := m.addItem(item); err != nil {
			return err
		}
	}
	return nil
}

// bindRun prepares terminal presentation and resolves defaults once projected items are available.
func (m *multichoice) bindRun(ctx context.Context, tio *termIO) error {
	tio.refreshSize()
	if tio.Width < 1 || tio.Height < 3 {
		return ErrNoSpace
	}
	err := m.bind(defaultIO.bindTheme(ctx, m.theme, m.out, tio))
	if err != nil {
		return err
	}
	m.label, m.help, err = m.theme.templates.Multichoice.chrome(m.look.funcs, m.Label)
	if err != nil {
		return err
	}
	m.label = m.label.beforeNewline()
	if m.loading {
		return nil
	}
	return m.resolveDefault()
}

// projectItem caches searchable content, deferring details and non-searchable rows until drawing.
func (m *multichoice) projectItem(item any) (text, error) {
	content, err := m.projectContent(m.slot.value, item)
	if err != nil {
		return nil, err
	}
	m.content = append(m.content, content)
	m.details = append(m.details, nil)
	m.drawn = append(m.drawn, false)
	return content, nil
}

// drawItem caches details on first display.
func (m *multichoice) drawItem(i int) (err error) {
	if m.drawn[i] {
		return nil
	}
	m.details[i], err = m.projectDetails(m.slot.details, m.Items[i])
	if err != nil {
		return err
	}
	m.drawn[i] = true
	return nil
}

// themedRow wraps state-specific content in the Row slot that owns focus and check marks.
func (m *multichoice) themedRow(i int, active bool) (text, error) {
	err := m.drawItem(i)
	if err != nil {
		return nil, err
	}
	slot := m.slot.inactive
	if active {
		slot = m.slot.active
	}
	value, err := slot.render(choiceView{
		Value:    m.content[i].String(),
		Details:  m.details[i].String(),
		Index:    i,
		Active:   active,
		Selected: m.selected[i],
	})
	if err != nil {
		return nil, err
	}
	return m.slot.row.render(multichoiceRow{value.String(), m.selected[i], active, i})
}

// renderThemed fits themed rows to the terminal, ending each with a newline for frame clearing.
func (m *multichoice) renderThemed(tio *termIO, buf *bytes.Buffer) error {
	tio.refreshSize()
	if tio.Width < 1 || tio.Height < 3 {
		return ErrNoSpace
	}
	m.clampRows(tio.Height / 2)
	rows := make([]frameRow, 0, len(m.displayed)+4)
	rows = append(rows, frameRow{content: m.label, drop: 1})
	if m.noMatches() {
		empty, err := m.slot.empty.render(emptyView{m.typed})
		if err != nil {
			return err
		}
		if empty.width() > 0 {
			rows = append(rows, frameRow{content: empty, guided: true})
		}
	}
	for row, i := range m.displayed {
		line, err := m.themedRow(i, row == m.active)
		if err != nil {
			return err
		}
		rows = append(rows, frameRow{content: line, guided: true})
	}
	help, err := themedHint(m.help, m.multichoiceKeys(m.theme.source.Symbols), len(m.relevant)-m.offset-len(m.displayed))
	if err != nil {
		return err
	}
	// on a short terminal, the hint with the count goes first, then the label
	rows = append(rows, frameRow{content: help, guided: true, drop: 2})
	// the newline after the last row moves the cursor to a row of its own,
	// which must stay on the screen, or the terminal scrolls the label away
	fit, err := m.look.geometry(tio.Width, tio.Height-1).fit(rows)
	if err != nil {
		return err
	}
	for _, line := range fit.lines {
		buf.WriteByte('\r')
		buf.Write(line)
		buf.WriteByte('\n')
	}
	buf.WriteByte('\r')
	return nil
}

// themedAnswer formats checked projections in input order, retaining nothing for a hidden or empty answer.
func (m *multichoice) themedAnswer() error {
	values := make([]string, 0, m.count)
	for _, i := range m.indices() {
		values = append(values, m.content[i].String())
	}
	body, err := m.slot.answerValue.render(collectionView{values, len(values)})
	if err != nil {
		return err
	}
	out, err := m.slot.answer.render(
		answerView{Label: m.Label, Value: body.String(), Values: values, Count: len(values)},
	)
	if err != nil || out.width() == 0 {
		return err
	}
	return m.writeAnswer(append(out, '\n'))
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

// selection reports whether d is a slice of the target type, and returns its members.
func (c choiceTarget) selection(d any) ([]any, bool) {
	if d == nil {
		return nil, false
	}
	rv := reflect.ValueOf(d)
	if rv.Kind() != reflect.Slice || !rv.Type().AssignableTo(reflect.SliceOf(c.elem())) {
		return nil, false
	}
	members := make([]any, rv.Len())
	for i := range members {
		members[i] = rv.Index(i).Interface()
	}
	return members, true
}

// resolveSelection resolves a default of a multichoice to checked indexes in
// source order: one entry, a slice of entries of the target type, or a string hint.
func (c choiceTarget) resolveSelection(d any, items []any, hint func(string) ([]int, error)) ([]int, error) {
	if c.accepts(d) {
		// the whole value is one entry first, as T may itself be a slice
		matches, err := c.matches(d, items)
		if err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			i, err := uniqueMatch(matches, fmt.Sprintf("%T value", d))
			if err != nil {
				return nil, err
			}
			return []int{i}, nil
		}
	}
	if members, ok := c.selection(d); ok {
		picked := make([]int, 0, len(members))
		for _, member := range members {
			matches, err := c.matches(member, items)
			if err != nil {
				return nil, err
			}
			i, err := uniqueMatch(matches, "selection member")
			if err != nil {
				return nil, err
			}
			if slices.Contains(picked, i) {
				return nil, errBadDefault("selection repeats an entry")
			}
			picked = append(picked, i)
		}
		slices.Sort(picked)
		return picked, nil
	}
	s, ok := d.(string)
	if !ok {
		return nil, errBadDefault("%T matches no entry", d)
	}
	matches, err := hint(s)
	if err != nil {
		return nil, err
	}
	i, err := uniqueMatch(matches, fmt.Sprintf("hint %q", s))
	if err != nil {
		return nil, err
	}
	return []int{i}, nil
}
