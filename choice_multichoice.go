// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/trie"
	"github.com/nfx/go-tui/theme"
	"io"
	"iter"
	"reflect"
	"slices"
	"sort"
	"strings"
)

type multichoice struct {
	Ctx   context.Context
	Label string
	Items []any

	projection

	// theme is the snapshot captured at construction. The multichoice
	// renders through it: look is its binding to the destination,
	// slot the bound slots, label the row rendered once per run and help
	// the bound hint rendered every frame with the count of hidden items.
	theme *render.Compiled
	look  render.Binding
	slot  multichoiceSlots
	label ansi.Text
	help  render.BoundSlot
	// pending holds the items until binding projects them.
	pending []any
	// content is the projected main content of every item. Its details
	// render when a frame first draws it, which drawn records.
	content, details []ansi.Text
	drawn            []bool

	trie      *trie.Trie
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
	m.trie = trie.NewTrie()
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
		score = choiceScore(render.ChoiceLabels{}.PlainText(search), m.hintRunes)
	}
	m.scores = append(m.scores, score)
	matches := true
	if m.typed != "" {
		single := trie.NewTrie()
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
func (m *multichoice) indexItem(item any) (ansi.Text, error) {
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
	return m.target.resolveSelection(m.def.raw, m.Items, trie.HintFromTrie(m.trie))
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

// multichoiceKeys are the bindings beyond confirming that the hint row describes.
func (m *multichoice) multichoiceKeys(s theme.Symbols) []render.Hint {
	return []render.Hint{
		{Left: s.Arrows(), Right: ansi.Text("move")},
		{Left: ansi.Text("space"), Right: ansi.Text("toggle")},
		{Left: ansi.Text("type"), Right: ansi.Text("filter")},
	}
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
