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
	"unicode/utf8"
)

type dropdownOutputEvent interface {
	isDropdownOutputEvent()
}

type dropdownOutputEventMarker struct{}

func (dropdownOutputEventMarker) isDropdownOutputEvent() {}

type dropdownInit struct {
	dropdownOutputEventMarker
	Label string
}

type dropdownAppendItem struct {
	dropdownOutputEventMarker
	Item  any
	Index int
	Text  string
}

type dropdownFilterChanged struct {
	dropdownOutputEventMarker
	Prefix   string
	Matching int
	Added    []int
	Removed  []int
}

type dropdownConfirmed struct {
	dropdownOutputEventMarker
	Selected int
}

type dropdownState struct {
	prefix   string
	relevant []int
}

type dropdownInputEvent interface {
	isDropdownInputEvent()
}

type dropdownInputEventMarker struct{}

func (dropdownInputEventMarker) isDropdownInputEvent() {}

type dropdownInputConfirmed struct {
	dropdownInputEventMarker
	Index int
}

type dropdownFilteredWith struct {
	dropdownInputEventMarker
	Prefix string
}

type dropdown struct {
	choiceLabels
	Ctx          context.Context
	Label        string
	Items        []any
	trie         *trie
	active       []text
	activeWidths []int
	inactive     []text
	widths       []int
	relevant     []int
	displayed    []int
	Default      any
	Hide         bool
	OneReturn    bool

	LabelTemplate        string
	labelTemplate        *template.Template
	ActiveItemTemplate   string
	activeItemTemplate   *template.Template
	InactiveItemTemplate string
	inactiveItemTemplate *template.Template
	MoreItemsTemplate    string
	moreItemsTemplate    *template.Template
	AnswerTemplate       string
	answerTemplate       *template.Template
	// labelSet, answerSet, activeSet, inactiveSet and moreSet record per-call
	// templates, which win over the theme in any option order.
	labelSet, answerSet, activeSet, inactiveSet, moreSet bool
	projection

	// theme is the snapshot captured at construction. The dropdown renders
	// through it: family are the slots of the widget that
	// runs it, look is its binding to the destination, slot the bound
	// slots, label and hint the rows rendered once per run. A wrapper that
	// runs several dropdowns hands the binding of an earlier one on as
	// inherit, so that one invocation keeps one appearance.
	theme       *compiledTheme
	family      choiceFamily
	look        binding
	inherit     binding
	slot        choiceSlots
	label, hint text
	// content is the projected main content of every item, and search the
	// plain text of it that filtering and ranking see.
	content []text
	search  []string
	// rows is the height of the last frame.
	rows int

	ItemsFn func(prefix string) []any

	IterBatchSize int
	IterFn        func(any, error) bool
	iterDone      bool
	itItems       chan itPair

	selected int
	offset   int
	// moved is set once the user moves the selection with arrow keys, and
	// cleared when filtering starts over at the top of the list.
	moved    bool
	typed    []rune
	oneMatch string // see [WithDefault]
	// def is the raw default; target types it; normalize adapts it first.
	def       defaultValue
	target    choiceTarget
	normalize func(any) any
	// seedIdx is the entry a typed default selects when seeded is set.
	seedIdx  int
	seeded   bool
	seedLazy bool
	// rankHint keeps the [WithDefault] value for ranking every candidate set,
	// including items that are added lazily, after oneMatch is consumed.
	rankHint string
	// longest is the widest active or inactive item seen so far.
	longest int

	in  io.Reader
	out io.Writer

	// TODO: special case for testing?..
	makeTermIO func(in io.Reader, out io.Writer) (*termIO, error)
	eventSink  func(dropdownOutputEvent)
	input      <-chan dropdownInputEvent
}

type itPair struct {
	item any
	err  error
}

type lazyResult struct {
	index       int
	done        bool
	needsRender bool
	readNextKey bool
}

var confirmRunner = defaultConfirmRunner

// Confirmf is [Confirm] with a formatted action.
func Confirmf(ctx context.Context, format string, a ...any) bool {
	return Confirm(ctx, fmt.Sprintf(format, a...))
}

// Confirm asks to choose Yes or No and reports whether Yes was chosen. No is a
// normal answer, retained like Yes; an error also returns false.
func Confirm(ctx context.Context, action string, opts ...opt) bool {
	return confirmRunner(ctx, action, opts...)
}

// defaultConfirmRunner runs a dropdown over Yes and No through the Confirm slots.
func defaultConfirmRunner(ctx context.Context, action string, o ...opt) bool {
	d := newDropdown(ctx)
	d.family = d.theme.templates.Confirm
	d.target = targetOf[string]()
	d.normalize = confirmDefault
	err := opts(o).Apply(d)
	if err != nil {
		return false
	}
	i, err := d.pick(ctx, action, []any{"Yes", "No"}) // Yes is first
	if err != nil {
		return false
	}
	return i == 0
}

// confirmDefault turns a boolean default into the text of its choice.
func confirmDefault(d any) any {
	if d == nil {
		return d
	}
	rv := reflect.ValueOf(d)
	if rv.Kind() != reflect.Bool {
		return d
	}
	if rv.Bool() {
		return "Yes"
	}
	return "No"
}

type mapKV[K comparable, V any] struct {
	Key   K
	Value V
}

// mapKey projects the key of a map entry that the map widgets built.
func mapKey[K comparable, V any](item any) any {
	kv, _ := item.(mapKV[K, V]) //nolint:errcheck // the widget only holds its own entries
	return kv.Key
}

// mapValue projects the value of a map entry that the map widgets built.
func mapValue[K comparable, V any](item any) any {
	kv, _ := item.(mapKV[K, V]) //nolint:errcheck // the widget only holds its own entries
	return kv.Value
}

func DropdownKV[K comparable, V any](ctx context.Context, label string, items map[K]V, opts ...opt) (K, V, error) {
	var zeroK K
	var zeroV V
	if len(items) == 0 {
		return zeroK, zeroV, ErrNoItems
	}
	kvs := make([]mapKV[K, V], 0, len(items))
	for k, v := range items {
		kvs = append(kvs, mapKV[K, V]{k, v})
	}
	sort.Slice(kvs, func(i, j int) bool {
		// sort by key, use fmt to convert to string
		return fmt.Sprintf("%v", kvs[i].Key) < fmt.Sprintf("%v", kvs[j].Key)
	})
	// the theme presents the entries, projected to their keys
	opts = append([]opt{WithTemplate("(label .Key)")}, opts...)
	anyKVs := make([]any, len(kvs))
	for i, kv := range kvs {
		anyKVs[i] = kv
	}
	target := choiceTarget{
		typ:  reflect.TypeFor[K](),
		proj: mapKey[K, V],
	}
	i, err := pickIndex(ctx, label, anyKVs, target, opts)
	if err != nil {
		return zeroK, zeroV, err
	}
	return kvs[i].Key, kvs[i].Value, nil
}

func Dropdown[T any](ctx context.Context, label string, items []T, opts ...opt) (T, error) {
	var zero T
	if len(items) == 0 {
		return zero, ErrNoItems
	}
	// apparently, there's no other non-reflective way around
	anyItems := make([]any, len(items))
	for i, v := range items {
		anyItems[i] = v
	}
	i, err := pickIndex(ctx, label, anyItems, targetOf[T](), opts)
	if err != nil {
		return zero, err
	}
	// we know i is valid
	return items[i], nil
}

func DropdownLazy[V any](ctx context.Context, label string, itemFn iter.Seq2[V, error], o ...opt) (V, error) {
	var zero V
	d := newDropdown(ctx)
	d.Label = label
	d.target = targetOf[V]()
	err := opts(o).Apply(d)
	if err != nil {
		return zero, err
	}
	// validate everything that can fail before the producer starts
	err = d.parseTemplates()
	if err != nil {
		return zero, fmt.Errorf("templates: %w", err)
	}
	seq := iter.Seq2[any, error](func(yield func(any, error) bool) {
		for v, err := range itemFn {
			if !yield(v, err) {
				return
			}
		}
	})
	terminal := outputIsTerminal(d.out)
	// the producer is owned by this call: it stops on every return path
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.Ctx = ctx
	var i int
	if terminal {
		d.startLazyProducer(ctx, seq)
		i, err = d.selectIndex(ctx)
	} else {
		i, err = d.answerLazyFromDefault(ctx, seq)
	}
	if err != nil {
		return zero, err
	}
	item := d.Items[i]
	if terminal {
		err = d.answer(label, i)
		if err != nil {
			return zero, fmt.Errorf("answer: %w", err)
		}
	}
	valid, ok := item.(V)
	if !ok && item != nil { // should never happen
		return zero, fmt.Errorf("%w: expected %T, got %T", ErrInvalidState, valid, item)
	}
	return valid, nil
}

// answerLazyFromDefault answers without a terminal. The producer starts only
// when a default or the single-item shortcut can answer, and is then consumed
// to the end, so that a later item cannot hide an ambiguity.
func (d *dropdown) answerLazyFromDefault(ctx context.Context, seq iter.Seq2[any, error]) (int, error) {
	err := ctx.Err()
	if err != nil {
		return -1, err
	}
	if !d.def.set && !d.OneReturn {
		return -1, errNoDefault(d.Label)
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

func DropdownIndex(ctx context.Context, label string, items []any, o ...opt) (int, error) {
	return pickIndex(ctx, label, items, choiceTarget{}, o)
}

// pickIndex runs a dropdown whose typed defaults have the type of target.
func pickIndex(ctx context.Context, label string, items []any, target choiceTarget, o []opt) (int, error) {
	d := newDropdown(ctx)
	d.target = target
	err := opts(o).Apply(d)
	if err != nil {
		return -1, err
	}
	return d.pick(ctx, label, items)
}

// pick runs a configured dropdown over items and shows the answer.
func (d *dropdown) pick(ctx context.Context, label string, items []any) (int, error) {
	d.Label = label
	d.Items = items
	i, err := d.dropdownIndex(ctx)
	if err != nil {
		return -1, err
	}
	if !outputIsTerminal(d.out) {
		return i, nil // an automatic answer prints nothing
	}
	err = d.answer(label, i)
	if err != nil {
		return -1, fmt.Errorf("answer: %w", err)
	}
	return i, nil
}

// answer prints the answer line for item i: the answer slot of the theme,
// unless a per-call template takes the raw item.
func (d *dropdown) answer(label string, i int) error {
	if d.answerSet {
		return d.showAnswer(label, d.Items[i])
	}
	return d.themedAnswer(label, i)
}

func (d *dropdown) showAnswer(label string, item any) error {
	if d.Hide {
		return nil
	}
	var buf bytes.Buffer
	err := d.answerTemplate.Execute(&buf, dropdownAnswer{
		Label:  label,
		Answer: item,
	})
	if err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	_, err = buf.WriteTo(d.out)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (d *dropdown) dropdownIndex(ctx context.Context) (int, error) {
	// we must always parse templates, because we may still
	// want to show the answer in the call after this one.
	err := d.parseTemplates()
	if err != nil {
		return -1, fmt.Errorf("templates: %w", err)
	}
	return d.selectIndex(ctx)
}

// selectIndex runs a dropdown with parsed templates and returns the original
// item index. [dropdown.run] returns a position in d.relevant instead.
func (d *dropdown) selectIndex(ctx context.Context) (int, error) {
	if !outputIsTerminal(d.out) {
		err := ctx.Err()
		if err != nil {
			return -1, err
		}
		i, err := d.resolveAutomatic(d.Items)
		if err != nil {
			return -1, err
		}
		return i, ctx.Err()
	}
	if d.OneReturn && len(d.Items) == 1 {
		return 0, nil
	}
	err := d.seedDefault()
	if err != nil {
		return -1, err
	}
	j, err := d.run(ctx)
	if err != nil {
		return -1, err
	}
	return d.relevant[j], nil
}

// resolveAutomatic picks the entry that answers without a terminal. The
// single-item shortcut of [WithOneReturn] wins over a default.
func (d *dropdown) resolveAutomatic(items []any) (int, error) {
	if d.OneReturn && len(items) == 1 {
		return 0, nil
	}
	if !d.def.set {
		return -1, errNoDefault(d.Label)
	}
	return d.target.resolveSingle(d.def.raw, items, d.hintMatches(items))
}

// hintMatches looks up prefix hints in the labels of items without drawing them.
func (d *dropdown) hintMatches(items []any) func(string) ([]int, error) {
	return func(hint string) ([]int, error) {
		t := newTrie()
		for i, item := range items {
			label, err := d.searchText(item)
			if err != nil {
				return nil, err
			}
			t.Add(label, i)
		}
		return hintFromTrie(t)(hint)
	}
}

// searchText returns searchable item content without theme decoration.
func (d *dropdown) searchText(item any) (text, error) {
	if !d.legacySearch() {
		err := d.bindOnce()
		if err != nil {
			return nil, err
		}
		return d.projectContent(d.slot.value, item)
	}
	var label text
	err := d.inactiveItemTemplate.Execute(&label, item)
	if err != nil {
		return nil, fmt.Errorf("inactive: %w", err)
	}
	return label, nil
}

// legacySearch reports whether a dropdown matches the output of a
// per-call inactive template, as before the theme, because no per-call
// projection replaces it.
func (d *dropdown) legacySearch() bool { return d.inactiveSet && d.mainTemplate == nil }

// seedDefault makes a typed default the first and selected row of an
// interactive dropdown. String hints keep their own ranking.
func (d *dropdown) seedDefault() error {
	if !d.def.set {
		return nil
	}
	if _, isHint := d.def.raw.(string); isHint {
		return nil
	}
	if !d.target.accepts(d.def.raw) {
		return errBadDefault("%T has the wrong type", d.def.raw)
	}
	if d.itItems != nil {
		d.seedLazy = true // the entry may arrive later
		return nil
	}
	matches, err := d.target.matches(d.def.raw, d.Items)
	if err != nil {
		return err
	}
	i, err := uniqueMatch(matches, fmt.Sprintf("%T value", d.def.raw))
	if err != nil {
		return err
	}
	d.seedIdx, d.seeded = i, true
	return nil
}

// seedFirst moves the seeded entry to the front of relevant.
func (d *dropdown) seedFirst(relevant []int) {
	if !d.seeded {
		return
	}
	pos := slices.Index(relevant, d.seedIdx)
	if pos > 0 {
		copy(relevant[1:pos+1], relevant[:pos])
		relevant[0] = d.seedIdx
	}
}

type dropdownAnswer struct {
	Label  string
	Answer any
}

func WithOneReturn() opt {
	return opT(func(d *dropdown) error {
		d.OneReturn = true
		return nil
	})
}

// WithHide skips printing the answer of a dropdown or an input.
func WithHide() opt {
	return opT(func(x withPrompt) error {
		x.setHide()
		return nil
	})
}

// WithFieldTemplate uses named fields for choice rows and answers.
//
// Deprecated: use [WithTemplate] for simpler and more flexible approach.
func WithFieldTemplate(fieldName ...string) opt {
	return opT(func(d withChoiceContent) error {
		d.setFieldTemplate(fieldName...)
		return nil
	})
}

// setFieldTemplate keeps row fields and the single-item answer in sync.
func (d *dropdown) setFieldTemplate(fieldName ...string) {
	single := make([]string, 0, len(fieldName))
	for _, f := range fieldName {
		single = append(single, "."+f)
	}
	d.setProjection(strings.Join(single, ` ", " `))
}

// WithTemplate shares main content across choice rows and answers; details appear only on the active row.
func WithTemplate(main string, activeDetails ...string) opt {
	return opT(func(d withChoiceContent) error {
		d.setContentTemplate(main, activeDetails...)
		return nil
	})
}

// setContentTemplate keeps active-only details out of inactive rows and answers.
func (d *dropdown) setContentTemplate(main string, activeDetails ...string) {
	d.setProjection(main, activeDetails...)
}

// projection is the content of a choice list set by [WithTemplate]
// or [WithFieldTemplate]. It replaces the Value and Details slots of the
// theme and leaves the presentation alone. view adapts a raw item to the
// payload of those slots; nil passes it unchanged. Per-call templates always
// take the raw item.
type projection struct {
	mainSource, detailsSource     string
	mainTemplate, detailsTemplate *template.Template
	view                          func(any) any
}

// slotItem is the payload of the Value and Details slots for item.
func (p *projection) slotItem(item any) any {
	if p.view == nil {
		return item
	}
	return p.view(item)
}

// setProjection preserves WithTemplate expressions while leaving presentation to the theme.
func (p *projection) setProjection(main string, details ...string) {
	p.mainSource = fmt.Sprintf(`{{ __content %s }}`, main)
	p.detailsSource = ""
	if len(details) > 0 {
		p.detailsSource = fmt.Sprintf(`{{ __content (%s) }}`, strings.Join(details, `) ", " (`))
	}
}

// WithLabelTemplate overrides the prompt label of a dropdown or an input.
func WithLabelTemplate(tmpl string) opt {
	return opT(func(x withPrompt) error {
		x.setLabelTemplate(tmpl)
		return nil
	})
}

// WithActiveItemTemplate overrides content for the focused choice.
func WithActiveItemTemplate(tmpl string) opt {
	return opT(func(d withActiveItemTemplate) error {
		d.setActiveItemTemplate(tmpl)
		return nil
	})
}

// WithInactiveItemTemplate overrides content for unfocused choices.
func WithInactiveItemTemplate(tmpl string) opt {
	return opT(func(d withInactiveItemTemplate) error {
		d.setInactiveItemTemplate(tmpl)
		return nil
	})
}

// WithMoreItemsTemplate overrides the footer for choices outside the visible window.
func WithMoreItemsTemplate(tmpl string) opt {
	return opT(func(d withMoreItemsTemplate) error {
		d.setMoreItemsTemplate(tmpl)
		return nil
	})
}

// WithAnswerTemplate overrides the answer line of a dropdown or an input.
func WithAnswerTemplate(tmpl string) opt {
	return opT(func(x withPrompt) error {
		x.setAnswerTemplate(tmpl)
		return nil
	})
}

func newDropdown(ctx context.Context) *dropdown {
	in, out := defaultStreams(ctx)
	theme := defaultIO.currentTheme()
	return &dropdown{
		in:            in,
		out:           out,
		Ctx:           ctx,
		Label:         "Select from list",
		makeTermIO:    makePromptIO,
		IterBatchSize: 10,
		trie:          newTrie(),
		theme:         theme,
		family:        theme.templates.Dropdown,
	}
}

// choiceLabels owns the shared label contract without widget interaction state.
type choiceLabels struct{}

// itemLabel gives choice widgets a consistent label for display and search.
func (d choiceLabels) itemLabel(item any) string {
	label, ok := d.structLabel(item)
	if ok {
		return label
	}
	label, ok = d.stringerLabel(item)
	if ok {
		return label
	}
	return fmt.Sprint(item)
}

// rowLabel flattens item labels to fit single-line choice slots.
func (d choiceLabels) rowLabel(item any) string { return text(d.itemLabel(item)).oneLine() }

// plainText removes terminal styling and preserves word boundaries for ranking.
func (choiceLabels) plainText(t text) string {
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

// structLabel extracts a label from struct items using tags and common field names.
func (d choiceLabels) structLabel(item any) (string, bool) {
	value, ok := d.indirectValue(reflect.ValueOf(item))
	if !ok || value.Kind() != reflect.Struct {
		return "", false
	}
	label, ok := d.annotatedStructLabel(value)
	if ok {
		return label, true
	}
	return d.heuristicStructLabel(value)
}

// stringerLabel returns String() output, including pointer-receiver methods on struct values.
func (d choiceLabels) stringerLabel(item any) (string, bool) {
	x, ok := item.(fmt.Stringer)
	if ok {
		return x.String(), true
	}
	value, ok := d.indirectValue(reflect.ValueOf(item))
	if !ok || value.Kind() != reflect.Struct {
		return "", false
	}
	ptr := reflect.New(value.Type())
	ptr.Elem().Set(value)
	x, ok = ptr.Interface().(fmt.Stringer)
	if !ok {
		return "", false
	}
	return x.String(), true
}

// annotatedStructLabel resolves fields explicitly marked as label.
func (d choiceLabels) annotatedStructLabel(value reflect.Value) (string, bool) {
	rt := value.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		if !field.IsExported() || !d.isLabelTag(field.Tag) {
			continue
		}
		label, ok := d.valueLabel(value.Field(i))
		if ok {
			return label, true
		}
	}
	return "", false
}

// heuristicStructLabel resolves labels from common field names.
func (d choiceLabels) heuristicStructLabel(value reflect.Value) (string, bool) {
	rt := value.Type()
	for _, want := range []string{
		"label",
		"name",
		"title",
		"description",
		"displayname",
		"fullname",
		"summary",
		"text",
		"subject",
	} {
		for i := range rt.NumField() {
			field := rt.Field(i)
			if !field.IsExported() || !strings.EqualFold(field.Name, want) {
				continue
			}
			label, ok := d.valueLabel(value.Field(i))
			if ok {
				return label, true
			}
		}
	}
	return "", false
}

// valueLabel stringifies an annotated or heuristic field and skips empty values.
func (d choiceLabels) valueLabel(value reflect.Value) (string, bool) {
	value, ok := d.indirectValue(value)
	if !ok {
		return "", false
	}
	if value.Kind() == reflect.String {
		out := strings.TrimSpace(value.String())
		if out != "" {
			return out, true
		}
		return "", false
	}
	if value.CanInterface() {
		x, ok := value.Interface().(fmt.Stringer)
		if ok {
			out := strings.TrimSpace(x.String())
			if out != "" {
				return out, true
			}
			return "", false
		}
	}
	if value.Kind() == reflect.Struct {
		ptr := reflect.New(value.Type())
		ptr.Elem().Set(value)
		x, ok := ptr.Interface().(fmt.Stringer)
		if ok {
			out := strings.TrimSpace(x.String())
			if out != "" {
				return out, true
			}
			return "", false
		}
	}
	return "", false
}

// isLabelTag recognizes explicit label annotations shared by choice widgets.
func (d choiceLabels) isLabelTag(tag reflect.StructTag) bool {
	return strings.EqualFold(strings.TrimSpace(tag.Get("header")), "label") ||
		d.hasLabelOption(tag.Get("tui")) ||
		strings.EqualFold(strings.TrimSpace(tag.Get("label")), "true")
}

// hasLabelOption checks whether a comma-separated tag contains "label".
func (d choiceLabels) hasLabelOption(tag string) bool {
	for option := range strings.SplitSeq(tag, ",") {
		if strings.EqualFold(strings.TrimSpace(option), "label") {
			return true
		}
	}
	return false
}

// indirectValue dereferences interface/pointer wrappers and rejects nil values.
func (d choiceLabels) indirectValue(value reflect.Value) (reflect.Value, bool) {
	if !value.IsValid() {
		return reflect.Value{}, false
	}
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	return value, true
}

func (d *dropdown) parseTemplates() error {
	return d.parseThemed()
}

// parseThemed parses per-call templates and projections for later destination binding.
func (d *dropdown) parseThemed() error {
	return parseThemed(d.theme, "dropdown", []perCall{
		{d.labelSet, "label", mustEndWith(d.LabelTemplate, ' '), &d.labelTemplate},
		{d.activeSet, "active", d.ActiveItemTemplate, &d.activeItemTemplate},
		{d.inactiveSet, "inactive", d.InactiveItemTemplate, &d.inactiveItemTemplate},
		{d.moreSet, "more", d.MoreItemsTemplate, &d.moreItemsTemplate},
		{d.answerSet, "answer", mustEndWith(d.AnswerTemplate, '\n'), &d.answerTemplate},
		{d.mainSource != "", "content", d.mainSource, &d.mainTemplate},
		{d.detailsSource != "", "details", d.detailsSource, &d.detailsTemplate},
	})
}

// perCall is a per-call template of a themed choice list, parsed into dest
// only when the option set it.
type perCall struct {
	set          bool
	name, source string
	dest         **template.Template
}

// parseThemed checks explicitly set templates using helpers replaced at destination binding.
func parseThemed(c *compiledTheme, name string, parts []perCall) error {
	funcs := c.funcs(profilePlain, roleStyles{})
	funcs["__content"] = span{}.helper()
	tmpl := template.New(name).Funcs(funcs)
	for _, part := range parts {
		if !part.set {
			continue
		}
		parsed, err := tmpl.New(part.name).Parse(part.source)
		if err != nil {
			return fmt.Errorf("%s: %w", part.name, err)
		}
		*part.dest = parsed
	}
	return nil
}

// rebind clones non-nil templates before installing destination-bound helpers.
func rebind(funcs template.FuncMap, ts ...**template.Template) error {
	for _, t := range ts {
		if *t == nil {
			continue
		}
		clone, err := (*t).Clone()
		if err != nil {
			return fmt.Errorf("%s: %w", (*t).Name(), err)
		}
		*t = clone.Funcs(funcs)
	}
	return nil
}

// choiceSlots are the theme slots of a choice family bound to one destination.
type choiceSlots struct {
	value, details, active, inactive, more, answer boundSlot
}

// bind binds the item and answer slots of the family to funcs.
func (f choiceFamily) bind(funcs template.FuncMap) (s choiceSlots, _ error) {
	var errs [6]error
	s.value, errs[0] = f.Value.bind(funcs)
	s.details, errs[1] = f.Details.bind(funcs)
	s.active, errs[2] = f.Active.bind(funcs)
	s.inactive, errs[3] = f.Inactive.bind(funcs)
	s.more, errs[4] = f.More.bind(funcs)
	s.answer, errs[5] = f.Answer.bind(funcs)
	return s, errors.Join(errs[:]...)
}

// chrome renders label and key hints, passing the raw label to an explicit template.
func (f choiceFamily) chrome(
	funcs template.FuncMap,
	explicit *template.Template,
	label string,
	keys []keyHint,
) (head, hint text, _ error) {
	if explicit != nil {
		err := explicit.Execute(&head, label)
		if err != nil {
			return nil, nil, fmt.Errorf("label: %w", err)
		}
	} else {
		slot, err := f.Label.bind(funcs)
		if err != nil {
			return nil, nil, err
		}
		head, err = slot.render(labelView{label})
		if err != nil {
			return nil, nil, err
		}
	}
	slot, err := f.Hint.bind(funcs)
	if err != nil {
		return nil, nil, err
	}
	hint, err = slot.render(hintView{keys})
	return head, hint, err
}

// dropdownKeys are the bindings beyond confirming that the hint row describes.
var dropdownKeys = []keyHint{{Key: "up/down", Action: "move"}, {Key: "type", Action: "filter"}}

// bind installs destination-bound helpers and choice slots.
func (d *dropdown) bind(look binding) error {
	d.look = look
	err := rebind(look.funcs, &d.labelTemplate, &d.activeItemTemplate, &d.inactiveItemTemplate,
		&d.moreItemsTemplate, &d.answerTemplate, &d.mainTemplate, &d.detailsTemplate)
	if err != nil {
		return err
	}
	d.slot, err = d.family.bind(look.funcs)
	return err
}

// bindOnce binds to the destination without a terminal lease unless a run
// already bound: for automatic answers, and the answer of a skipped prompt.
func (d *dropdown) bindOnce() error {
	if d.look.theme != nil {
		return nil
	}
	return d.bind(d.destination(d.Ctx, nil))
}

// destination is the inherited binding, or binds the theme to the output,
// probing through tio when it holds the prompt lease.
func (d *dropdown) destination(ctx context.Context, tio *termIO) binding {
	if d.inherit.theme != nil {
		return d.inherit
	}
	return defaultIO.bindTheme(ctx, d.theme, d.out, tio)
}

// renderChrome renders the label and hint rows once per run.
func (d *dropdown) renderChrome() (err error) {
	d.label, d.hint, err = d.family.chrome(d.look.funcs, d.labelTemplate, d.Label, dropdownKeys)
	return err
}

// projectContent renders the per-call projection or falls back to the bound Value slot.
func (p *projection) projectContent(value boundSlot, item any) (text, error) {
	if p.mainTemplate == nil {
		return value.render(p.slotItem(item))
	}
	var out text
	err := p.mainTemplate.Execute(&out, item)
	if err != nil {
		return nil, fmt.Errorf("content: %w", err)
	}
	return out.beforeNewline(), nil
}

// projectDetails uses projected details when a projection is set, otherwise the bound Details slot.
func (p *projection) projectDetails(details boundSlot, item any) (text, error) {
	switch {
	case p.mainTemplate == nil:
		return details.render(p.slotItem(item))
	case p.detailsTemplate == nil:
		return nil, nil
	}
	var out text
	err := p.detailsTemplate.Execute(&out, item)
	if err != nil {
		return nil, fmt.Errorf("details: %w", err)
	}
	return out.beforeNewline(), nil
}

// choiceView is the payload of the Active and Inactive slots: the projected
// content of one item, its source Index and its state. A dropdown has no
// check state, so Selected is false.
type choiceView struct {
	Value, Details   string
	Index            int
	Active, Selected bool
}

// themedItem caches both item rows and returns projected content for filtering.
// Explicit templates own their markers; theme slots use the renderer's focus mark.
func (d *dropdown) themedItem(i int, item any) (text, error) {
	content, err := d.projectContent(d.slot.value, item)
	if err != nil {
		return nil, err
	}
	details, err := d.projectDetails(d.slot.details, item)
	if err != nil {
		return nil, err
	}
	view := choiceView{Value: content.String(), Details: details.String(), Index: i, Active: true}
	d.active[i], err = d.themedRow(d.activeItemTemplate, d.slot.active, item, view)
	if err != nil {
		return nil, err
	}
	view.Active = false
	d.inactive[i], err = d.themedRow(d.inactiveItemTemplate, d.slot.inactive, item, view)
	if err != nil {
		return nil, err
	}
	search := content
	if d.legacySearch() {
		search = d.inactive[i]
	}
	d.content[i] = content
	d.search[i] = d.plainText(search)
	return search, nil
}

// themedRow renders one state of an item on a single line.
func (d *dropdown) themedRow(explicit *template.Template, slot boundSlot, item any, view choiceView) (text, error) {
	if explicit != nil {
		var out text
		err := explicit.Execute(&out, item)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", explicit.Name(), err)
		}
		return out.beforeNewline(), nil
	}
	out, err := slot.render(view)
	if err != nil {
		return nil, err
	}
	mark := text("  ")
	if view.Active {
		mark = append(d.look.style(d.look.roles.accent, text(d.theme.source.Symbols.Focus)), ' ')
	}
	return append(mark, out...), nil
}

// themedAnswer renders the answer slot with the projected content of item i,
// projecting it now when the prompt answered without drawing it. A hidden or
// empty answer retains nothing.
func (d *dropdown) themedAnswer(label string, i int) error {
	if d.Hide {
		return nil
	}
	err := d.bindOnce()
	if err != nil {
		return err
	}
	var content text
	if i < len(d.content) {
		content = d.content[i]
	} else if content, err = d.projectContent(d.slot.value, d.Items[i]); err != nil {
		return err
	}
	return d.retain(label, content.String())
}

// retain writes the answer slot for a formatted value, such as the path that
// several dropdowns of a [FilePicker] chose; a hidden or empty answer writes
// nothing.
func (d *dropdown) retain(label, value string) error {
	if d.Hide {
		return nil
	}
	err := d.bindOnce()
	if err != nil {
		return err
	}
	out, err := d.slot.answer.render(answerView{Label: label, Value: value, Values: []string{value}, Count: 1})
	if err != nil || out.width() == 0 {
		return err
	}
	_, err = d.out.Write(append(out, '\n'))
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// mustEndWith ensures a terminator even when the template is empty.
func mustEndWith(base string, r byte) string {
	if len(base) == 0 || base[len(base)-1] != r {
		base += string(r)
	}
	return base
}

// implement [withPrompt] interface.
func (d *dropdown) setHide() {
	d.Hide = true
}

// implement [withPrompt] interface.
func (d *dropdown) setLabelTemplate(tmpl string) {
	d.LabelTemplate = tmpl
	d.labelSet = true
}

// implement [withPrompt] interface.
func (d *dropdown) setAnswerTemplate(tmpl string) {
	d.AnswerTemplate = tmpl
	d.answerSet = true
}

// implement [withPrompt] interface.
func (d *dropdown) setDefault(v any) {
	if d.normalize != nil {
		v = d.normalize(v)
	}
	d.def = defaultValue{set: true, raw: v}
	hint, _ := v.(string) //nolint:errcheck // not a string is no hint
	d.oneMatch = hint
	d.rankHint = hint
}

// implements [withIO].
func (d *dropdown) setReader(r io.Reader) {
	d.in = r
}

// implements [withIO].
func (d *dropdown) setWriter(w io.Writer) {
	tui, ok := w.(*Tui)
	if ok {
		w = tui.prependView(10) // TODO: height is properly available only after render, right?...
	}
	d.out = w
}

type oneHatch int //nolint:errname // hack for [WithDefault]

func (i oneHatch) Error() string {
	return fmt.Sprintf("%d", i)
}

// clampDisplay derives the visible rows from current terminal height and
// scrolls them after a resize, so that the selected item stays selected.
func (d *dropdown) clampDisplay(height int) {
	if len(d.displayed) == 0 || len(d.relevant) == 0 {
		return
	}
	capacity := max(1, min(len(d.relevant), height/2))
	pos := min(d.offset+d.selected, len(d.relevant)-1)
	d.offset = min(d.offset, pos, len(d.relevant)-capacity)
	d.offset = max(d.offset, pos-capacity+1)
	d.displayed = d.relevant[d.offset : d.offset+capacity]
	d.selected = pos - d.offset
}

func (d *dropdown) renderInit(io *termIO) (longest int, err error) {
	if len(d.displayed) > 0 {
		return d.longest, nil // already initialized
	}
	d.emit(dropdownInit{Label: d.Label})
	d.trie = newTrie()
	d.active = make([]text, len(d.Items))
	d.activeWidths = make([]int, len(d.Items))
	d.inactive = make([]text, len(d.Items))
	d.widths = make([]int, len(d.Items))
	d.content = make([]text, len(d.Items))
	d.search = make([]string, len(d.Items))
	d.relevant = make([]int, len(d.Items))
	d.longest = 0
	if d.oneMatch != "" {
		d.rankHint = d.oneMatch
	}
	for i, item := range d.Items {
		err = d.setItem(i, item)
		if err != nil {
			return d.longest, fmt.Errorf("add item: %w", err)
		}
		d.relevant[i] = i
	}
	if d.oneMatch != "" && len(d.Items) > 0 {
		// this is a hack to make [WithDefault] + [WithOneReturn] equivalent
		// work for dropdowns.
		matched := d.trie.Prefix(d.oneMatch)
		match := d.oneMatch
		d.oneMatch = ""
		if len(matched) == 1 {
			// this may properly work only with all items known upfront,
			// as lazily added items might yield more than one match at
			// some undetermined point in the future.
			// run results are positions in d.relevant; it is still in item
			// order here, as ranking is skipped for a unique prefix.
			return d.longest, oneHatch(slices.Index(d.relevant, matched[0]))
		}
		d.sortRelevantByLevenstein(match)
	}
	d.seedFirst(d.relevant)
	d.displayed = d.relevant[:min(len(d.relevant), io.Height/2)]
	return d.longest, nil
}

func (d *dropdown) sortRelevantByLevenstein(match string) {
	d.rankByLevenstein(d.relevant, match)
}

// maxRankRunes bounds the per-label work of similarity ranking: only the
// first maxRankRunes runes of a label and of the hint are compared, so one
// comparison costs at most maxRankRunes² steps and linear memory.
const maxRankRunes = 256

// rankByLevenstein ranks by hint similarity, breaking ties by input order.
// Cancellation leaves the current order unchanged.
func (d *dropdown) rankByLevenstein(relevant []int, match string) {
	want := []rune(strings.ToLower(match))
	scores := make(map[int]int, len(relevant))
	for _, i := range relevant {
		if d.Ctx.Err() != nil {
			return
		}
		scores[i] = choiceScore(d.rankLabel(i), want)
	}
	sort.SliceStable(relevant, func(i, j int) bool {
		a, b := relevant[i], relevant[j]
		if scores[a] == scores[b] {
			return a < b
		}
		return scores[a] < scores[b]
	})
}

// rankLabel is the text of item i that ranking compares: its projected content.
func (d *dropdown) rankLabel(i int) string { return d.search[i] }

// choiceDistance bounds Unicode edit-distance work for interactive ranking.
func choiceDistance(a, b []rune) int {
	a = a[:min(len(a), maxRankRunes)]
	b = b[:min(len(b), maxRankRunes)]
	if len(b) > len(a) {
		a, b = b, a // rows are as wide as the shorter sequence
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// levenstein keeps dropdown ranking on the shared choice-distance contract.
func (*dropdown) levenstein(a, b []rune) int { return choiceDistance(a, b) }

// setItem renders and indexes item at i.
func (d *dropdown) setItem(i int, item any) error {
	search, err := d.renderStates(i, item)
	if err != nil {
		return err
	}
	d.trie.Add(search, i)
	d.activeWidths[i] = d.active[i].width()
	d.widths[i] = d.inactive[i].width()
	d.longest = max(d.longest, d.widths[i], d.activeWidths[i])
	d.emit(dropdownAppendItem{
		Item:  item,
		Index: i,
		Text:  d.inactive[i].String(),
	})
	return nil
}

// renderStates caches the active and inactive rows of item at i and returns
// the text that filtering matches.
func (d *dropdown) renderStates(i int, item any) (text, error) {
	return d.themedItem(i, item)
}

type dropdownMore struct {
	More  int
	Total int
}

// render draws the label, the visible rows, the count of rows below them
// and the hint in the geometry of the theme. Every row ends with a newline,
// so that [termIO.clear] erases the frame.
func (d *dropdown) render(io *termIO, buf *bytes.Buffer) error {
	io.refreshSize()
	_, err := d.renderInit(io)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	d.clampDisplay(io.Height)
	more, err := d.themedMore()
	if err != nil {
		return fmt.Errorf("more: %w", err)
	}
	// the newline after the last row moves the cursor to a row of its own,
	// which must stay on the screen, or the terminal scrolls the label away
	g := d.look.geometry(io.Width, io.Height-1)
	rows := make([]frameRow, 0, len(d.displayed)+4)
	// Inline, the label leads the first row and indents the others, unless
	// the widest row does not fit after it.
	var lead, indent text
	if d.label.width() > 0 {
		lead = append(text{}, d.label...)
		if !d.labelSet {
			// the renderer owns the spacing after a theme label; a per-call
			// label keeps its own trailing space, as in Input
			lead = append(lead, ' ')
		}
	}
	if g.inline && lead.width()+d.longest <= g.columns(nil) {
		indent = text(strings.Repeat(" ", lead.width()))
	} else {
		lead = nil
		rows = append(rows, frameRow{content: d.label, drop: 1})
	}
	for k, j := range d.displayed {
		row, prefix := d.inactive[j], indent
		if k == d.selected {
			row = d.active[j]
		}
		if k == 0 {
			prefix = lead
		}
		rows = append(rows, frameRow{content: append(append(text{}, prefix...), row...), guided: true})
	}
	if more.width() > 0 {
		rows = append(rows, frameRow{content: append(append(text{}, indent...), more...), guided: true, drop: 2})
	}
	// on a short terminal, the hint goes first, then the count, then the label
	rows = append(rows, frameRow{content: d.hint, guided: true, drop: 2})
	fit, err := g.fit(rows)
	if err != nil {
		return err
	}
	for _, line := range fit.lines {
		buf.WriteByte('\r')
		buf.Write(line)
		buf.WriteByte('\n')
	}
	buf.WriteByte('\r')
	d.rows = len(fit.lines)
	return nil
}

// themedMore renders the count of the rows below the visible ones, if any.
func (d *dropdown) themedMore() (text, error) {
	more := len(d.relevant) - d.offset - len(d.displayed)
	if more <= 0 {
		return nil, nil
	}
	view := dropdownMore{More: more, Total: len(d.relevant)}
	if d.moreItemsTemplate == nil {
		return d.slot.more.render(view)
	}
	var out text
	err := d.moreItemsTemplate.Execute(&out, view)
	if err != nil {
		return nil, err
	}
	return out.beforeNewline(), nil
}

func (d *dropdown) height() int {
	return d.rows
}

// snapshotState captures dropdown state so key handlers can emit semantic diffs.
func (d *dropdown) snapshotState() dropdownState {
	// ranking may reorder relevant, while Diff needs numerically sorted indexes
	relevant := append([]int(nil), d.relevant...)
	slices.Sort(relevant)
	return dropdownState{
		prefix:   string(d.typed),
		relevant: relevant,
	}
}

// emitStateChanges publishes filter changes after a state transition.
func (d *dropdown) emitStateChanges(prev dropdownState) {
	curr := d.snapshotState()
	if curr.Equal(prev) {
		return
	}
	added, removed := curr.Diff(prev)
	d.emit(dropdownFilterChanged{
		Prefix:   curr.prefix,
		Matching: len(curr.relevant),
		Added:    added,
		Removed:  removed,
	})
}

// Equal compares two dropdown state snapshots.
func (d dropdownState) Equal(other dropdownState) bool {
	if d.prefix != other.prefix {
		return false
	}
	if len(d.relevant) != len(other.relevant) {
		return false
	}
	for i := range d.relevant {
		if d.relevant[i] != other.relevant[i] {
			return false
		}
	}
	return true
}

// Diff returns added and removed relevant indexes between two sorted snapshots.
func (d dropdownState) Diff(prev dropdownState) (added []int, removed []int) {
	var i, j int
	for i < len(prev.relevant) && j < len(d.relevant) {
		a := prev.relevant[i]
		b := d.relevant[j]
		if a == b {
			i++
			j++
			continue
		}
		if a < b {
			removed = append(removed, a)
			i++
			continue
		}
		added = append(added, b)
		j++
	}
	for ; i < len(prev.relevant); i++ {
		removed = append(removed, prev.relevant[i])
	}
	for ; j < len(d.relevant); j++ {
		added = append(added, d.relevant[j])
	}
	return added, removed
}

// emit sends a typed dropdown output event to the optional sink.
func (d *dropdown) emit(ev dropdownOutputEvent) {
	if d.eventSink == nil {
		return
	}
	d.eventSink(ev)
}

// decodeInputEvent maps terminal keys into semantic dropdown input actions.
func (d *dropdown) decodeInputEvent(key rune) dropdownInputEvent {
	switch key {
	case keyEnter:
		i := -1
		pos := d.offset + d.selected
		if pos >= 0 && pos < len(d.relevant) {
			i = d.relevant[pos]
		}
		return dropdownInputConfirmed{Index: i}
	case 0x7f: // backspace
		next := []rune(string(d.typed))
		if len(next) > 0 {
			next = next[:len(next)-1]
		}
		return dropdownFilteredWith{Prefix: string(next)}
	default:
		return dropdownFilteredWith{Prefix: string(d.typed) + string(key)}
	}
}

var ErrNoSpace = errors.New("no space in terminal")

// run returns the position of the chosen item in d.relevant, not its original index.
func (d *dropdown) run(ctx context.Context) (int, error) {
	io, err := d.makeTermIO(d.in, d.out)
	if err != nil {
		return -1, fmt.Errorf("raw term: %w", err)
	}
	defer io.Restore() //nolint:errcheck // we can't do much about it here
	if io.Height < 3 {
		return -1, ErrNoSpace
	}
	// before the event reader starts, as binding may probe the background
	err = d.bind(d.destination(ctx, io))
	if err == nil {
		err = d.renderChrome()
	}
	if err != nil {
		return -1, err
	}
	frame := bytes.NewBuffer(make([]byte, d.height()*io.Width))
	frame.Reset()
	if d.itItems != nil {
		return d.runLazy(ctx, io, frame)
	}
	for {
		i, err := d.runRender(ctx, io, frame)
		if err != nil {
			return -1, err
		}
		if i >= 0 {
			return i, nil
		}
	}
}

// runLazy renders the dropdown while items are streamed in.
func (d *dropdown) runLazy(ctx context.Context, tio *termIO, frame *bytes.Buffer) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	keys := tio.readEvents(ctx, nil)
	defer func() {
		cancel()
		// wait without draining, so an unreceived key stays for the next prompt
		tio.awaitReader()
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
			keys = tio.readEvents(ctx, nil)
		}
		needsRender = res.needsRender
	}
}

// ensureLazyRender draws the dropdown only when it needs a refresh.
func (d *dropdown) ensureLazyRender(
	tio *termIO,
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
	tio *termIO,
	frame *bytes.Buffer,
	space int,
	displayed int,
	keys <-chan keyEvent,
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
	case <-tio.onResize:
		return lazyResult{needsRender: true}, nil
	}
}

func (d *dropdown) nextLazyKeyResult(
	tio *termIO,
	frame *bytes.Buffer,
	space int,
	displayed int,
	ev keyEvent,
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
func (d *dropdown) renderLazyFrame(tio *termIO, frame *bytes.Buffer, space *int, displayed *int) error {
	if *space > 0 {
		err := tio.clear(*space, frame)
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

// ErrEmptyLazyResult is empty lazy result.
var ErrEmptyLazyResult = errors.New("empty lazy result")

func (d *dropdown) finishLazyItems(tio *termIO, frame *bytes.Buffer, space int) (int, bool, error) {
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
	matches, err := d.target.matches(d.def.raw, d.Items)
	if err != nil {
		return err
	}
	i, err := uniqueMatch(matches, fmt.Sprintf("%T value", d.def.raw))
	if err != nil {
		return err
	}
	d.seedIdx, d.seeded = i, true
	return nil
}

// handleLazyItem updates the dropdown for a streamed item.
func (d *dropdown) handleLazyItem(
	tio *termIO,
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
	tio *termIO,
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
	tio *termIO,
	frame *bytes.Buffer,
	space int,
	displayed int,
	ev keyEvent,
	ok bool,
) (int, bool, error) {
	if !ok {
		err := d.clearFrame(tio, frame, space)
		if err != nil {
			return -1, false, errors.Join(io.EOF, err)
		}
		return -1, false, io.EOF
	}
	if ev.err != nil {
		if isPasteText(ev.err) {
			return -1, false, nil
		}
		readErr := fmt.Errorf("read: %w", ev.err)
		clearErr := d.clearFrame(tio, frame, space)
		if clearErr != nil {
			return -1, false, errors.Join(readErr, clearErr)
		}
		return -1, false, readErr
	}
	i := d.pressKeyRune(tio, ev.key, displayed, space)
	if i >= 0 {
		err := d.clearFrame(tio, frame, space)
		if err != nil {
			return -1, false, err
		}
		return i, false, nil
	}
	return -1, true, nil
}

func (d *dropdown) runRender(ctx context.Context, io *termIO, frame *bytes.Buffer) (int, error) {
	err := d.render(io, frame)
	if err != nil {
		var oneMatch oneHatch
		ok := errors.As(err, &oneMatch)
		if ok {
			return int(oneMatch), nil
		}
		return -1, fmt.Errorf("render: %w", err)
	}
	_, err = frame.WriteTo(io)
	if err != nil {
		return -1, fmt.Errorf("write: %w", err)
	}
	space := d.height()
	displayed := len(d.displayed)
	select {
	case it, more := <-d.itItems:
		err := d.loadItem(io, frame, it, more, space)
		return -1, err
	case <-d.Ctx.Done():
		err = io.clear(space, frame)
		if err != nil {
			return -1, fmt.Errorf("clear: %w", err)
		}
		_, err = frame.WriteTo(io)
		if err != nil {
			return -1, fmt.Errorf("write: %w", err)
		}
		return -1, d.Ctx.Err()
	default:
		return d.runMain(ctx, io, frame, space, displayed)
	}
}

func (d *dropdown) runMain(ctx context.Context, io *termIO, frame *bytes.Buffer, space, displayed int) (int, error) {
	i, err := d.pressKey(ctx, io, frame, space, displayed)
	if isPasteText(err) {
		// Ctrl+V or CMD+V pressed
		return -1, nil
	} else if err != nil {
		frame.WriteTo(io) //nolint:errcheck // we can't do much about it here
		return -1, err
	}
	if i < 0 {
		return -1, nil
	}
	err = d.clearFrame(io, frame, space)
	if err != nil {
		return -1, err
	}
	return i, nil
}

func (d *dropdown) loadItem(io *termIO, frame *bytes.Buffer, it itPair, more bool, space int) error {
	if !more {
		d.iterDone = true
		d.itItems = nil
		return nil
	}
	if it.err != nil {
		errs := []error{it.err}
		err := io.clear(space, frame)
		if err != nil {
			errs = append(errs, fmt.Errorf("clear: %w", err))
		}
		_, err = frame.WriteTo(io)
		if err != nil {
			errs = append(errs, fmt.Errorf("write: %w", err))
		}
		return errors.Join(errs...)
	}
	err := d.addItem(io.Height, it.item)
	if err != nil {
		return err
	}
	var errs []error
	err = io.clear(space, frame)
	if err != nil {
		errs = append(errs, fmt.Errorf("clear: %w", err))
	}
	_, err = frame.WriteTo(io)
	if err != nil {
		errs = append(errs, fmt.Errorf("write: %w", err))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// addItem appends an item and refreshes derived state. Until the user moves
// the selection, it stays on the first row, which is the best-ranked item.
func (d *dropdown) addItem(height int, item any) error {
	var current = -1 // original index of the item the user moved to
	if pos := d.offset + d.selected; d.moved && pos >= 0 && pos < len(d.relevant) {
		current = d.relevant[pos]
	}
	d.Items = append(d.Items, item)
	d.active = append(d.active, nil)
	d.activeWidths = append(d.activeWidths, 0)
	d.inactive = append(d.inactive, nil)
	d.widths = append(d.widths, 0)
	d.content = append(d.content, nil)
	d.search = append(d.search, "")
	err := d.setItem(len(d.Items)-1, item)
	if err != nil {
		return fmt.Errorf("set item: %w", err)
	}
	if d.seedLazy && !d.seeded {
		matches, err := d.target.matches(d.def.raw, []any{item})
		d.seeded = err == nil && len(matches) == 1
		d.seedIdx = len(d.Items) - 1
	}
	d.relevant = d.matching(string(d.typed))
	capacity := max(1, height/2)
	if pos := slices.Index(d.relevant, current); current >= 0 && pos >= 0 {
		// ranking may reorder rows, keep the user on the same item
		d.offset = min(d.offset, pos)
		if pos-d.offset >= capacity {
			d.offset = pos - capacity + 1
		}
		d.selected = pos - d.offset
	}
	d.displayed = d.relevant[min(d.offset, len(d.relevant)):]
	d.displayed = d.displayed[:min(len(d.displayed), capacity)]
	return nil
}

// matching returns relevant item indexes for the prefix, ranked by
// the [WithDefault] hint when there is one.
func (d *dropdown) matching(prefix string) []int {
	relevant := d.trie.Prefix(prefix)
	if d.rankHint != "" {
		d.rankByLevenstein(relevant, d.rankHint)
	}
	d.seedFirst(relevant)
	return relevant
}

// pressKeyRune updates dropdown state for an already-read key.
func (d *dropdown) pressKeyRune(io *termIO, key rune, displayed, space int) int {
	switch key {
	case '↑':
		d.pressUp(displayed)
		return -1
	case '↓':
		d.pressDown(displayed)
		return -1
	case keyEscape, keyIgnored:
		return -1
	}
	ev := d.decodeInputEvent(key)
	return d.applyInputEvent(io, ev, displayed, space)
}

func (d *dropdown) applyInputEvent(io *termIO, ev dropdownInputEvent, displayed, space int) int {
	prev := d.snapshotState()
	switch typed := ev.(type) {
	case dropdownInputConfirmed:
		if typed.Index < 0 || typed.Index >= len(d.Items) {
			return -1
		}
		pos := -1
		for i, idx := range d.relevant {
			if idx == typed.Index {
				pos = i
				break
			}
		}
		if pos < 0 {
			return -1
		}
		d.emit(dropdownConfirmed{Selected: typed.Index})
		return pos
	case dropdownFilteredWith:
		done := d.filterWith(typed.Prefix, displayed, space, io.Height/2)
		if done {
			d.emitStateChanges(prev)
			d.emit(dropdownConfirmed{Selected: d.relevant[0]})
			return 0
		}
	default:
		d.emitStateChanges(prev)
		return -1
	}
	d.emitStateChanges(prev)
	return -1
}

// pressKeyFromInput reads the next event from the pre-supplied input channel and applies it.
func (d *dropdown) pressKeyFromInput(tio *termIO, frame *bytes.Buffer, space, displayed int) (int, error) {
	var ev dropdownInputEvent
	select {
	case e, ok := <-d.input:
		if !ok {
			return -1, io.EOF
		}
		ev = e
	case <-tio.onResize:
		return -1, tio.clear(space, frame)
	}
	err := tio.clear(space, frame)
	if err != nil {
		return -1, err
	}
	i := d.applyInputEvent(tio, ev, displayed, space)
	if i >= 0 {
		// the caller clears the frame once; a second clear would start at
		// its top and erase the rows above it
		frame.Reset()
		return i, nil
	}
	return -1, nil
}

// pressKey applies the next key, or clears the frame on resize so the caller
// redraws with the new geometry.
func (d *dropdown) pressKey(
	ctx context.Context,
	tio *termIO,
	frame *bytes.Buffer,
	space, displayed int,
) (i int, err error) {
	if d.input != nil {
		return d.pressKeyFromInput(tio, frame, space, displayed)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	keys := tio.readEvents(ctx, nil)
	var ev keyEvent
	select {
	case e, ok := <-keys:
		tio.awaitReader()
		if !ok {
			return -1, d.Ctx.Err()
		}
		ev = e
	case <-tio.onResize:
		cancel()
		// an unreceived key stays with the input for the next read
		tio.awaitReader()
		return -1, tio.clear(space, frame)
	}
	if ev.err != nil {
		return -1, ev.err
	}
	err = tio.clear(space, frame)
	if err != nil {
		return -1, err
	}
	i = d.pressKeyRune(tio, ev.key, displayed, space)
	if i >= 0 {
		// the caller clears the frame once, see pressKeyFromInput
		frame.Reset()
		return i, nil
	}
	return -1, nil
}

// clearFrame removes the last render without drawing a new frame.
func (d *dropdown) clearFrame(io *termIO, frame *bytes.Buffer, space int) error {
	if space < 1 {
		return nil
	}
	if io.vp != nil && io.vp.fixedHeight {
		err := io.clear(space, io)
		if err != nil {
			return fmt.Errorf("clear: %w", err)
		}
		return nil
	}
	err := io.clear(space, frame)
	if err != nil {
		return fmt.Errorf("clear: %w", err)
	}
	_, err = frame.WriteTo(io)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (d *dropdown) pressUp(displayed int) {
	d.moved = true
	if d.offset > 0 && d.selected == 0 { // page up
		d.offset--
		d.displayed = d.relevant[d.offset : d.offset+displayed]
	} else if d.selected > 0 {
		d.selected--
	}
}

func (d *dropdown) pressDown(displayed int) {
	d.moved = true
	if d.offset+displayed < len(d.relevant) { // page down
		d.offset++
		d.displayed = d.relevant[d.offset : d.offset+displayed]
	} else if d.selected < displayed-1 {
		d.selected++
	}
}

func (d *dropdown) pressBackspace(io *termIO) {
	if len(d.typed) == 0 {
		return
	}
	d.typed = d.typed[:len(d.typed)-1]
	d.relevant = d.matching(string(d.typed))
	d.displayed = d.relevant[:min(len(d.relevant), io.Height/2)]
	d.selected = 0
	d.offset = 0
	d.moved = false
}

// filterWith replaces the current filter text and refreshes matching rows.
func (d *dropdown) filterWith(text string, displayed, space, height int) bool {
	prevTyped := string(d.typed)
	prevRelevant := d.relevant
	d.typed = []rune(text)
	d.relevant = d.matching(text)
	if d.OneReturn && len(d.relevant) == 1 {
		return true
	}
	if len(d.relevant) == 0 {
		d.typed = []rune(prevTyped)
		d.relevant = prevRelevant
		return false
	}
	limit := min(len(d.relevant), displayed, space)
	if utf8.RuneCountInString(text) < utf8.RuneCountInString(prevTyped) {
		limit = min(len(d.relevant), height)
	}
	d.displayed = d.relevant[:limit]
	d.selected = 0
	d.offset = 0
	d.moved = false
	return false
}

func (d *dropdown) pressAny(key rune, displayed, space int) bool {
	d.typed = append(d.typed, key)
	d.relevant = d.matching(string(d.typed))
	if d.OneReturn && len(d.relevant) == 1 {
		return true
	}
	if len(d.relevant) == 0 {
		d.typed = d.typed[:len(d.typed)-1]
		d.relevant = d.matching(string(d.typed))
		return false
	}
	d.displayed = d.relevant[:min(len(d.relevant), displayed, space)]
	d.selected = 0
	d.offset = 0
	d.moved = false
	return false
}

// setActiveItemTemplate supports the shared focused-choice option.
func (d *dropdown) setActiveItemTemplate(tmpl string) {
	d.ActiveItemTemplate = tmpl
	d.activeSet = true
}

// setInactiveItemTemplate supports the shared unfocused-choice option.
func (d *dropdown) setInactiveItemTemplate(tmpl string) {
	d.InactiveItemTemplate = tmpl
	d.inactiveSet = true
}

// setMoreItemsTemplate supports the shared overflow-footer option.
func (d *dropdown) setMoreItemsTemplate(tmpl string) {
	d.MoreItemsTemplate = tmpl
	d.moreSet = true
}

// choiceScore compares labels case-insensitively with an already normalized hint.
func choiceScore(label string, hint []rune) int {
	return choiceDistance([]rune(strings.ToLower(label)), hint)
}

var anyType = reflect.TypeFor[any]()

// choiceTarget describes the type a typed default of a choice widget has: the
// type of the returned item, key or value, and how to project it from a source
// entry. A zero target accepts any value and matches whole entries.
type choiceTarget struct {
	typ  reflect.Type
	proj func(item any) any
}

// targetOf is the target of a widget returning T.
func targetOf[T any]() choiceTarget { return choiceTarget{typ: reflect.TypeFor[T]()} }

func (c choiceTarget) elem() reflect.Type {
	if c.typ == nil {
		return anyType
	}
	return c.typ
}

func (c choiceTarget) project(item any) any {
	if c.proj == nil {
		return item
	}
	return c.proj(item)
}

// nilable reports whether values of typ can be nil.
func (c choiceTarget) nilable(typ reflect.Type) bool {
	switch typ.Kind() { //nolint:exhaustive // only these kinds can be nil
	case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan,
		reflect.UnsafePointer:
		return true
	}
	return false
}

func (c choiceTarget) isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return c.nilable(rv.Type()) && rv.IsNil()
}

// accepts reports whether d is assignable to the target type. It does not coerce.
func (c choiceTarget) accepts(d any) bool {
	if d == nil {
		return c.nilable(c.elem())
	}
	return reflect.TypeOf(d).AssignableTo(c.elem())
}

// matches returns the indexes of entries whose projection equals d. The caller
// has checked [choiceTarget.accepts].
func (c choiceTarget) matches(d any, items []any) ([]int, error) {
	want := d
	if d != nil {
		rv := reflect.ValueOf(d)
		if rv.Kind() == reflect.Func && !rv.IsNil() {
			return nil, errBadDefault("%T is a function, which cannot be identified", d)
		}
		if typ := c.elem(); typ.Kind() != reflect.Interface {
			// assignable types may differ, e.g. a named slice and an unnamed one
			nv := reflect.New(typ).Elem()
			nv.Set(rv)
			want = nv.Interface()
		}
	}
	var out []int
	for i, item := range items {
		got := c.project(item)
		if d == nil {
			if c.isNil(got) {
				out = append(out, i)
			}
			continue
		}
		if c.equalValues(want, got) {
			out = append(out, i)
		}
	}
	return out, nil
}

// equalValues compares with Go equality when that is safe for the dynamic
// values, and structurally otherwise. Pointers compare by identity.
func (c choiceTarget) equalValues(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Type() != vb.Type() {
		return false
	}
	if va.Comparable() && vb.Comparable() {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}

// unique returns the only match, or the reason there is none.
func uniqueMatch(matches []int, what string) (int, error) {
	switch len(matches) {
	case 0:
		return -1, errBadDefault("%s matches no entry", what)
	case 1:
		return matches[0], nil
	}
	return -1, errBadDefault("%s matches %d entries", what, len(matches))
}

// resolveSingle finds the one entry a default identifies. A value of the target
// type is matched exactly first; a string without an exact match is a prefix hint.
func (c choiceTarget) resolveSingle(d any, items []any, hint func(string) ([]int, error)) (int, error) {
	if c.accepts(d) {
		matches, err := c.matches(d, items)
		if err != nil {
			return -1, err
		}
		if len(matches) > 0 {
			return uniqueMatch(matches, fmt.Sprintf("%T value", d))
		}
	}
	s, ok := d.(string)
	if !ok {
		return -1, errBadDefault("%T matches no entry", d)
	}
	matches, err := hint(s)
	if err != nil {
		return -1, err
	}
	return uniqueMatch(matches, fmt.Sprintf("hint %q", s))
}
