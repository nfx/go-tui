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
	"text/template"
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
	m.themed = themeRendering.Load()
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
	defaults := []opt{WithTemplate(".Key")}
	if themeRendering.Load() {
		// the theme presents the entries, projected to their keys
		defaults = []opt{WithTemplate("(label .Key)")}
	}
	return pickMultichoice(ctx, label, entries, target, append(defaults, o...))
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
	m.themed = themeRendering.Load()
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
	// labelSet, activeSet, inactiveSet and moreSet record per-call templates,
	// which win over the theme in any option order.
	labelSet, activeSet, inactiveSet, moreSet bool
	labelTemplate                             *template.Template
	projection

	// theme is the snapshot captured at construction. When themed, the
	// multichoice renders through it: look is its binding to the destination,
	// slot the bound slots, label and help the rows rendered once per run.
	theme       *compiledTheme
	themed      bool
	look        binding
	slot        multichoiceSlots
	label, help text
	// pending holds the items of a themed multichoice until binding projects them.
	pending []any
	// content is the projected main content of every item. Its details, and
	// its per-call rows in contents and inactive, render when a frame first
	// draws it, which drawn records.
	content, details []text
	drawn            []bool

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
		Ctx:                  ctx,
		Label:                "Select items",
		makeTermIO:           makePromptIO,
		LabelTemplate:        DefaultLabelTemplate,
		ActiveItemTemplate:   `{{label .}}`,
		InactiveItemTemplate: `{{label .}}`,
		ItemTemplate:         DefaultMultichoiceItemTemplate,
		MoreItemsTemplate:    DefaultMoreItemsTemplate,
		AnswerTemplate:       DefaultMultichoiceAnswerTemplate,
		theme:                defaultIO.currentTheme(),
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
func (m *multichoice) setLabelTemplate(t string) { m.LabelTemplate, m.labelSet = t, true }

// setAnswerTemplate configures the confirmed collection summary.
func (m *multichoice) setAnswerTemplate(t string) { m.AnswerTemplate, m.answerSet = t, true }

// setDefault records the default; a string is also a selection hint that
// does not pre-fill the filter.
func (m *multichoice) setDefault(d any) {
	m.def = defaultValue{set: true, raw: d}
	m.hint, _ = d.(string) //nolint:errcheck // not a string is no hint
}

// setActiveItemTemplate configures focused content independently of the checkbox wrapper.
func (m *multichoice) setActiveItemTemplate(t string) { m.ActiveItemTemplate, m.activeSet = t, true }

// setInactiveItemTemplate configures unfocused content independently of the checkbox wrapper.
func (m *multichoice) setInactiveItemTemplate(t string) {
	m.InactiveItemTemplate, m.inactiveSet = t, true
}

// setMoreItemsTemplate configures the footer for choices outside the visible window.
func (m *multichoice) setMoreItemsTemplate(t string) { m.MoreItemsTemplate, m.moreSet = t, true }

// setContentTemplate shares item content with collection answers, keeping details active-only.
func (m *multichoice) setContentTemplate(main string, details ...string) {
	if m.themed {
		m.setProjection(main, details...)
		return
	}
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
	if m.themed {
		m.setProjection(main)
		return
	}
	m.ActiveItemTemplate = fmt.Sprintf(`{{cyan %s}}`, main)
	m.InactiveItemTemplate = fmt.Sprintf(`{{print %s}}`, main)
	if !m.answerSet {
		m.AnswerTemplate = fmt.Sprintf(
			`{{dim "✔ " .Label " …"}} {{range $i, $item := .Answer}}{{if $i}}, {{end}}{{bold %s}}{{else}}none{{end}}`,
			main,
		)
	}
}

// prepare validates options and defers themed item projection until destination binding.
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
	if m.themed {
		m.pending = items // projected once bound to the destination
		return m.Ctx.Err()
	}
	for _, item := range items {
		if err := m.addItem(item); err != nil {
			return err
		}
	}
	return m.Ctx.Err()
}

// parseTemplates checks template syntax and caches the legacy label.
func (m *multichoice) parseTemplates() error {
	if m.themed {
		return parseThemed(m.theme, "multichoice", []perCall{
			{m.labelSet, "label", m.LabelTemplate, &m.labelTemplate},
			{m.activeSet, "active", m.ActiveItemTemplate, &m.activeItemTemplate},
			{m.inactiveSet, "inactive", m.InactiveItemTemplate, &m.inactiveItemTemplate},
			{m.moreSet, "more", m.MoreItemsTemplate, &m.moreItemsTemplate},
			{m.answerSet, "answer", mustEndWith(m.AnswerTemplate, '\n'), &m.answerTemplate},
			{m.mainSource != "", "content", m.mainSource, &m.mainTemplate},
			{m.detailsSource != "", "details", m.detailsSource, &m.detailsTemplate},
		})
	}
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
	if m.themed {
		return m.projectItem(item)
	}
	var active, inactive text
	err := m.activeItemTemplate.Execute(&active, item)
	if err != nil {
		return nil, fmt.Errorf("active item: %w", err)
	}
	err = m.inactiveItemTemplate.Execute(&inactive, item)
	if err != nil {
		return nil, fmt.Errorf("inactive item: %w", err)
	}
	m.contents = append(m.contents, active)
	m.inactive = append(m.inactive, inactive)
	return inactive, nil
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
	if m.themed {
		// without a terminal only the projection runs, for the hint to match
		err = m.bind(defaultIO.bindTheme(ctx, m.theme, m.out, nil))
		if err != nil {
			return err
		}
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
	if m.themed {
		return m.renderThemed(tio, buf)
	}
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
	if m.noMatches() {
		writeLine(text("No matches"))
	}
	for row, i := range m.displayed {
		content := m.inactive[i]
		if row == m.active {
			content = m.contents[i]
		}
		var decorated text
		if err := m.itemTemplate.Execute(&decorated, multichoiceRow{
			string(content), m.selected[i], row == m.active, i,
		}); err != nil {
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
	if m.themed && !m.answerSet {
		return m.themedAnswer()
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
	return m.writeAnswer(buf.Bytes())
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
var multichoiceKeys = []keyHint{
	{Key: "up/down", Action: "move"}, {Key: "space", Action: "toggle"}, {Key: "type", Action: "filter"},
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
	err := rebind(look.funcs, &m.labelTemplate, &m.activeItemTemplate, &m.inactiveItemTemplate,
		&m.moreItemsTemplate, &m.answerTemplate, &m.mainTemplate, &m.detailsTemplate)
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
	m.label, m.help, err = m.theme.templates.Multichoice.chrome(m.look.funcs, m.labelTemplate, m.Label, multichoiceKeys)
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
	search := content
	var inactive text
	if m.legacySearch() {
		inactive, err = m.perCallRow(m.inactiveItemTemplate, item)
		if err != nil {
			return nil, err
		}
		search = inactive
	}
	m.content = append(m.content, content)
	m.details = append(m.details, nil)
	m.contents = append(m.contents, nil)
	m.inactive = append(m.inactive, inactive)
	m.drawn = append(m.drawn, false)
	return search, nil
}

// legacySearch preserves inactive-template matching when no per-call projection replaces it.
func (m *multichoice) legacySearch() bool { return m.inactiveSet && m.mainTemplate == nil }

// perCallRow renders the per-call template t of a raw item on one line.
func (m *multichoice) perCallRow(t *template.Template, item any) (text, error) {
	var out text
	err := t.Execute(&out, item)
	if err != nil {
		return nil, fmt.Errorf("%s item: %w", t.Name(), err)
	}
	return out.beforeNewline(), nil
}

// drawItem caches details and per-call rows on first display.
func (m *multichoice) drawItem(i int) (err error) {
	if m.drawn[i] {
		return nil
	}
	item := m.Items[i]
	if m.activeItemTemplate == nil || m.inactiveItemTemplate == nil {
		m.details[i], err = m.projectDetails(m.slot.details, item)
		if err != nil {
			return err
		}
	}
	if m.activeItemTemplate != nil {
		m.contents[i], err = m.perCallRow(m.activeItemTemplate, item)
		if err != nil {
			return err
		}
	}
	if m.inactiveItemTemplate != nil && !m.legacySearch() {
		m.inactive[i], err = m.perCallRow(m.inactiveItemTemplate, item)
		if err != nil {
			return err
		}
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
	explicit, slot, value := m.inactiveItemTemplate, m.slot.inactive, m.inactive[i]
	if active {
		explicit, slot, value = m.activeItemTemplate, m.slot.active, m.contents[i]
	}
	if explicit == nil {
		value, err = slot.render(choiceView{
			Value:    m.content[i].String(),
			Details:  m.details[i].String(),
			Index:    i,
			Active:   active,
			Selected: m.selected[i],
		})
		if err != nil {
			return nil, err
		}
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
	more, err := m.themedMore()
	if err != nil {
		return fmt.Errorf("more: %w", err)
	}
	if more.width() > 0 {
		rows = append(rows, frameRow{content: more, guided: true, drop: 2})
	}
	// on a short terminal, the hint goes first, then the count, then the label
	rows = append(rows, frameRow{content: m.help, guided: true, drop: 2})
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

// themedMore renders the count of matching items below the visible window.
func (m *multichoice) themedMore() (text, error) {
	more := len(m.relevant) - m.offset - len(m.displayed)
	if more <= 0 {
		return nil, nil
	}
	view := dropdownMore{More: more, Total: len(m.relevant)}
	if m.moreItemsTemplate == nil {
		return m.slot.more.render(view)
	}
	var out text
	err := m.moreItemsTemplate.Execute(&out, view)
	if err != nil {
		return nil, err
	}
	return out.beforeNewline(), nil
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
	if !m.loading && !m.themed { // a themed multichoice resolves once bound
		if err := m.resolveDefault(); err != nil {
			return err
		}
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
	if m.themed {
		// before the event reader starts, as binding may probe the background
		if err := m.bindRun(ctx, tio); err != nil {
			return err
		}
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
