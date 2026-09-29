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
	"os"
	"reflect"
	"sort"
	"strings"
	"text/template"
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
	Ctx          context.Context
	Label        string
	Items        []any
	trie         *trie
	inactive     []bbuf
	widths       []int
	relevant     []int
	displayed    []int
	Default      any
	Hide         bool
	OneReturn    bool
	LabelNewLine bool

	LabelTemplate        string
	labelBuf             bytes.Buffer
	ActiveItemTemplate   string
	activeItemTemplate   *template.Template
	InactiveItemTemplate string
	inactiveItemTemplate *template.Template
	MoreItemsTemplate    string
	moreItemsTemplate    *template.Template
	AnswerTemplate       string
	answerTemplate       *template.Template

	ItemsFn func(prefix string) []any

	IterBatchSize int
	IterFn        func(any, error) bool
	iterDone      bool
	itItems       chan itPair

	selected int
	offset   int
	typed    []rune
	oneMatch string // see [WithDefault]

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

type keyEvent struct {
	key rune
	err error
}

type lazyResult struct {
	index       int
	done        bool
	needsRender bool
	readNextKey bool
}

var confirmRunner = defaultConfirmRunner

func Confirmf(format string, a ...any) bool {
	return Confirm(fmt.Sprintf(format, a...))
}

func Confirm(action string, opts ...opt) bool {
	return confirmRunner(action, opts...)
}

func defaultConfirmRunner(action string, opts ...opt) bool {
	res, err := Dropdown(action, []string{"Yes", "No"}, opts...)
	if err != nil {
		return false
	}
	return strings.EqualFold(res, "yes")
}

type mapKV[K comparable, V any] struct {
	Key   K
	Value V
}

func DropdownKV[K comparable, V any](label string, items map[K]V, opts ...opt) (K, V, error) {
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
	opts = append([]opt{
		WithActiveItemTemplate(`→ {{ bold .Key }}`),
		WithInactiveItemTemplate(`~ {{ dim .Key }}`),
		WithAnswerTemplate(`{{ dim "✔ " .Label " …" }} {{ .Answer.Key | bold }}`),
	}, opts...)
	item, err := Dropdown(label, kvs, opts...)
	if err != nil {
		return zeroK, zeroV, err
	}
	return item.Key, item.Value, nil
}

func Dropdown[T any](label string, items []T, opts ...opt) (T, error) {
	var zero T
	if len(items) == 0 {
		return zero, ErrNoItems
	}
	// apparently, there's no other non-reflective way around
	anyItems := make([]any, len(items))
	for i, v := range items {
		anyItems[i] = v
	}
	i, err := DropdownIndex(label, anyItems, opts...)
	if err != nil {
		return zero, err
	}
	// we know i is valid
	return items[i], nil
}

func DropdownLazy[V any](label string, itemFn iter.Seq2[V, error], o ...opt) (V, error) {
	var zero V
	d := newDropdown()
	d.Label = label
	err := opts(o).Apply(d)
	if err != nil {
		return zero, err
	}
	startDropdownLazyProducer(d, itemFn)
	i, err := d.dropdownIndex()
	if err != nil {
		return zero, err
	}
	item := d.Items[i]
	err = d.showAnswer(label, item)
	if err != nil {
		return zero, fmt.Errorf("answer: %w", err)
	}
	valid, ok := item.(V)
	if !ok { // should never happen
		return zero, fmt.Errorf("%w: expected %T, got %T", ErrInvalidState, valid, item)
	}
	return valid, nil
}

func startDropdownLazyProducer[V any](d *dropdown, itemFn iter.Seq2[V, error]) {
	d.itItems = make(chan itPair, max(1, d.IterBatchSize))
	go func() {
		defer close(d.itItems)
		select {
		case <-d.Ctx.Done():
			return
		default:
		}
		for v, err := range itemFn {
			select {
			case <-d.Ctx.Done():
				return
			case d.itItems <- itPair{v, err}:
				if err != nil {
					return
				}
			}
		}
	}()
}

func DropdownIndex(label string, items []any, o ...opt) (int, error) {
	d := newDropdown()
	d.Label = label
	d.Items = items
	i, err := d.dropdownIndex(o...)
	if err != nil {
		return -1, err
	}
	err = d.showAnswer(label, items[i])
	if err != nil {
		return -1, fmt.Errorf("answer: %w", err)
	}
	return i, nil
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

func (d *dropdown) dropdownIndex(o ...opt) (int, error) {
	err := opts(o).Apply(d)
	if err != nil {
		return -1, err
	}
	// we must always parse templates, because we may still
	// want to show the answer in the call after this one.
	err = d.parseTemplates()
	if err != nil {
		return -1, fmt.Errorf("templates: %w", err)
	}
	if d.OneReturn && len(d.Items) == 1 {
		return 0, nil
	}
	j, err := d.run()
	if err != nil {
		return -1, err
	}
	return d.relevant[j], nil
}

var DefaultLabelTemplate = `{{ "?" | green }} {{ . | bold }}`
var DefaultDropdownActiveItemTemplate = `{{ cyan "→ " (label .) }}`
var DefaultDropdownInactiveItemTemplate = `{{ dim "→ " (label .) }}`
var DefaultMoreItemsTemplate = ` {{ dim "↓ " .More " more … (" .Total " total)" | italic }}`
var DefaultAnswerTemplate = `{{ dim "✔ " .Label " …" }} {{ .Answer | bold }}`
var DefaultDropdownAnswerTemplate = `{{ dim "✔ " .Label " …" }} {{ bold (label .Answer) }}`

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

func WithHide() opt {
	return opT(func(d *dropdown) error {
		d.Hide = true
		return nil
	})
}

// Deprecated: use [WithTemplate] for simpler and more flexible approach.
func WithFieldTemplate(fieldName ...string) opt {
	return opT(func(d *dropdown) error {
		var single, answer []string
		for _, f := range fieldName {
			single = append(single, "."+f)
			answer = append(answer, ".Answer."+f)
		}
		a := strings.Join(single, ` ", " `)
		b := strings.Join(answer, ` ", " `)
		// d.LabelTemplate = fmt.Sprintf(`{{ "?" | green }} {{ .%s | bold }}`, fieldName)
		d.ActiveItemTemplate = fmt.Sprintf(`{{ cyan "→ " %s }}`, a)
		d.InactiveItemTemplate = fmt.Sprintf(`{{ dim "→ " %s }}`, a)
		d.AnswerTemplate = fmt.Sprintf(`{{ dim "✔ " .Label " …" }} {{ bold %s }}`, b)
		return nil
	})
}

// WithTemplate defines main field that is visible across active, inactive,
// and selected states; and activeDetails fields are visible in parentheses
// for active items.
func WithTemplate(main string, activeDetails ...string) opt {
	return opT(func(d *dropdown) error {
		details := ""
		if len(activeDetails) > 0 {
			details = strings.Join(activeDetails, `) ", " (`)
			details = fmt.Sprintf(` {{ dim "(" (%s) ")" }}`, details)
		}
		// TODO: add template validation once we have a reflection on field structure
		d.ActiveItemTemplate = fmt.Sprintf(`{{ cyan "→ " %s }}%s`, main, details)
		d.InactiveItemTemplate = fmt.Sprintf(`{{ dim "→ " %s }}`, main)
		d.AnswerTemplate = fmt.Sprintf(`{{ dim "✔ " .Label " …" }} {{ bold .Answer%s }}`, main)
		return nil
	})
}

func WithLabelTemplate(tmpl string) opt {
	return opT(func(d *dropdown) error {
		d.LabelTemplate = tmpl
		return nil
	})
}

func WithActiveItemTemplate(tmpl string) opt {
	return opT(func(d *dropdown) error {
		d.ActiveItemTemplate = tmpl
		return nil
	})
}

func WithInactiveItemTemplate(tmpl string) opt {
	return opT(func(d *dropdown) error {
		d.InactiveItemTemplate = tmpl
		return nil
	})
}

func WithMoreItemsTemplate(tmpl string) opt {
	return opT(func(d *dropdown) error {
		d.MoreItemsTemplate = tmpl
		return nil
	})
}

func WithAnswerTemplate(tmpl string) opt {
	return opT(func(d *dropdown) error {
		d.AnswerTemplate = tmpl
		return nil
	})
}

func newDropdown() *dropdown {
	return &dropdown{
		in:                   os.Stdin,
		out:                  os.Stderr,
		Ctx:                  context.Background(),
		Label:                "Select from list",
		makeTermIO:           makeTermIO,
		LabelTemplate:        DefaultLabelTemplate,
		ActiveItemTemplate:   DefaultDropdownActiveItemTemplate,
		InactiveItemTemplate: DefaultDropdownInactiveItemTemplate,
		MoreItemsTemplate:    DefaultMoreItemsTemplate,
		AnswerTemplate:       DefaultDropdownAnswerTemplate,
		IterBatchSize:        10,
		trie:                 newTrie(),
	}
}

// templateFuncs clones shared template funcs and injects dropdown-specific funcs.
func (d *dropdown) templateFuncs() template.FuncMap {
	funcs := make(template.FuncMap, len(colorFns)+1)
	for name, fn := range colorFns {
		funcs[name] = fn
	}
	funcs["label"] = d.itemLabel
	return funcs
}

// itemLabel renders a stable human label for dropdown items.
func (d *dropdown) itemLabel(item any) string {
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

// structLabel extracts a label from struct items using tags and common field names.
func (d *dropdown) structLabel(item any) (string, bool) {
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
func (d *dropdown) stringerLabel(item any) (string, bool) {
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
func (d *dropdown) annotatedStructLabel(value reflect.Value) (string, bool) {
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
func (d *dropdown) heuristicStructLabel(value reflect.Value) (string, bool) {
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
func (d *dropdown) valueLabel(value reflect.Value) (string, bool) {
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

// isLabelTag recognizes supported struct-tag annotations for dropdown labels.
func (d *dropdown) isLabelTag(tag reflect.StructTag) bool {
	return strings.EqualFold(strings.TrimSpace(tag.Get("header")), "label") ||
		d.hasLabelOption(tag.Get("tui")) ||
		strings.EqualFold(strings.TrimSpace(tag.Get("label")), "true")
}

// hasLabelOption checks whether a comma-separated tag contains "label".
func (d *dropdown) hasLabelOption(tag string) bool {
	for _, option := range strings.Split(tag, ",") {
		if strings.EqualFold(strings.TrimSpace(option), "label") {
			return true
		}
	}
	return false
}

// indirectValue dereferences interface/pointer wrappers and rejects nil values.
func (d *dropdown) indirectValue(value reflect.Value) (reflect.Value, bool) {
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
	tmpl := template.New("dropdown").Funcs(d.templateFuncs())
	labelTemplate, err := tmpl.New("label").Parse(mustEndWith(d.LabelTemplate, ' '))
	if err != nil {
		return fmt.Errorf("label: %w", err)
	}
	err = labelTemplate.Execute(&d.labelBuf, d.Label)
	if err != nil {
		return fmt.Errorf("label: %w", err)
	}
	d.activeItemTemplate, err = tmpl.New("active").Parse(mustEndWith(d.ActiveItemTemplate, '\n'))
	if err != nil {
		return fmt.Errorf("active: %w", err)
	}
	d.inactiveItemTemplate, err = tmpl.New("inactive").Parse(mustEndWith(d.InactiveItemTemplate, '\n'))
	if err != nil {
		return fmt.Errorf("inactive: %w", err)
	}
	d.moreItemsTemplate, err = tmpl.New("more").Parse(d.MoreItemsTemplate)
	if err != nil {
		return fmt.Errorf("more: %w", err)
	}
	d.answerTemplate, err = tmpl.New("answer").Parse(mustEndWith(d.AnswerTemplate, '\n'))
	if err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	return nil
}

func mustEndWith(base string, r byte) string {
	if base[len(base)-1] != r {
		base += string(r)
	}
	return base
}

// implements [withIO].
func (d *dropdown) setReader(r io.Reader) {
	d.in = r
}

// implements [withIO].
func (d *dropdown) setWriter(w io.Writer) {
	tui, ok := w.(*Tui)
	if ok {
		c := tui.prependView()
		c.height = 10             // TODO: this is properly available only after render, right?...
		c.next.height -= c.height // TODO: propagate down
		w = c
	}
	d.out = w
}

// implements [withContext].
func (d *dropdown) setContext(ctx context.Context) {
	d.Ctx = ctx
}

// implements [withContext].
func (d *dropdown) getContext() context.Context {
	return d.Ctx
}

type oneHatch int //nolint:errname // hack for [WithDefault]

func (i oneHatch) Error() string {
	return fmt.Sprintf("%d", i)
}

// render displays the dropdown.
func (d *dropdown) render(io *termIO, buf *bytes.Buffer) error {
	// use buffer to write to io only once
	longest, err := d.renderInit(io)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	var prefix int
	total := len(d.relevant)
	height := min(total, io.Height/2)
	bufMore, longest, err := d.renderMore(total, height, longest)
	if err != nil {
		return fmt.Errorf("more: %w", err)
	}
	for i, j := range d.displayed {
		buf.WriteByte('\r') // ensure we start from the leftmost position
		if i == 0 {
			prefix = d.renderLabel(buf, io, longest)
		} else {
			for range prefix {
				buf.WriteByte(' ')
			}
		}
		item, err := d.renderItem(io, i, j)
		if err != nil {
			return fmt.Errorf("item[i%d,j%d]: %w", i, j, err)
		}
		buf.Write(item)
	}
	if total > len(d.displayed) {
		buf.WriteByte('\r') // always display a line to avoid flickering
		if d.offset+height < total {
			for range prefix - 1 { // ???...
				buf.WriteByte(' ')
			}
			buf.Write(bufMore)
		}
		buf.WriteByte('\n')
	}
	buf.WriteByte('\r')
	return nil
}

func (d *dropdown) renderInit(io *termIO) (longest int, err error) {
	if len(d.displayed) > 0 {
		return 0, nil // already initialized
	}
	d.emit(dropdownInit{Label: d.Label})
	d.trie = newTrie()
	d.inactive = make([]bbuf, len(d.Items))
	d.widths = make([]int, len(d.Items))
	d.relevant = make([]int, len(d.Items))
	for i, item := range d.Items {
		err = d.setItem(i, item)
		if err != nil {
			return longest, fmt.Errorf("add item: %w", err)
		}
		longest = max(longest, d.widths[i])
	}
	if d.oneMatch != "" && len(d.Items) > 0 {
		d.sortRelevantByLevenstein(d.oneMatch)
		// this is a hack to make [WithDefault] + [WithOneReturn] equivalent
		// work for dropdowns.
		matched := d.trie.Prefix(d.oneMatch)
		d.oneMatch = ""
		if len(matched) == 1 {
			// this may properly work only with all items known upfront,
			// as lazily added items might yield more than one match at
			// some undetermined point in the future.
			return longest, oneHatch(matched[0])
		}
	}
	d.displayed = d.relevant[:min(len(d.relevant), io.Height/2)]
	return longest, nil
}

func (d *dropdown) sortRelevantByLevenstein(match string) {
	lookup := make(map[int]int, len(d.relevant))
	match = strings.ToLower(match)
	for _, i := range d.relevant {
		label := strings.ToLower(d.itemLabel(d.Items[i]))
		lookup[i] = d.levenstein(label, match)
	}
	sort.SliceStable(d.relevant, func(i, j int) bool {
		left := d.relevant[i]
		right := d.relevant[j]
		if lookup[left] == lookup[right] {
			return left < right
		}
		return lookup[left] < lookup[right]
	})
}

func (*dropdown) levenstein(a, b string) int {
	dist := make([][]int, len(a)+1)
	for i := range dist {
		dist[i] = make([]int, len(b)+1)
		dist[i][0] = i // a is the first column
	}
	for j := range dist[0] {
		dist[0][j] = j // b is the first row
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			dist[i][j] = min(
				dist[i-1][j]+1,      // deletion
				dist[i][j-1]+1,      // insertion
				dist[i-1][j-1]+cost, // substitution
			)
		}
	}
	return dist[len(a)][len(b)]
}

func (d *dropdown) setItem(i int, item any) error {
	err := d.inactiveItemTemplate.Execute(&d.inactive[i], item)
	if err != nil {
		return fmt.Errorf("inactive: %w", err)
	}
	inactive := d.inactive[i].String()
	d.trie.Add(inactive, i)
	d.widths[i] = width(d.inactive[i])
	d.relevant[i] = i
	d.emit(dropdownAppendItem{
		Item:  item,
		Index: i,
		Text:  inactive,
	})
	return nil
}

func (d *dropdown) renderLabel(buf *bytes.Buffer, io *termIO, longest int) int {
	label := d.labelBuf.Bytes()
	// TODO: we still have issues when label overflows the terminal width - some terminals wrap it, some don't.
	// proper solution would be to use viewports and scroll the label as well
	prefix := width(label)
	if prefix > io.Width {
		label = truncateVisible(label, io.Width-1, ' ')
	}
	buf.Write(label)
	if d.LabelNewLine || prefix+longest >= io.Width {
		buf.WriteByte('\n')
		buf.WriteByte('\r')
		d.LabelNewLine = true
		prefix = 0
	}
	return prefix
}

func (d *dropdown) renderItem(io *termIO, i, j int) (item bbuf, err error) {
	var itemW int
	if i == d.selected {
		// only active item is re-rendered
		err := d.activeItemTemplate.Execute(&item, d.Items[j])
		if err != nil {
			return nil, fmt.Errorf("active: %w", err)
		}
		itemW = width(item)
	} else {
		item = d.inactive[j]
		itemW = d.widths[j]
	}
	if itemW > io.Width {
		// this may fail if active item is wider than the terminal, but we can solve this later
		item = truncateVisible(item, io.Width-1, '\n')
	}
	return item, nil
}

func (d *dropdown) renderMore(total int, height int, longest int) (bbuf, int, error) {
	var bufMore bbuf
	if total <= len(d.displayed) {
		return bufMore, longest, nil // nothing to do
	}
	err := d.moreItemsTemplate.Execute(&bufMore, dropdownMore{
		More:  total - d.offset - height,
		Total: total,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("more: %w", err)
	}
	longest = max(longest, width(bufMore))
	return bufMore, longest, nil
}

type dropdownMore struct {
	More  int
	Total int
}

func (d *dropdown) height() int {
	// TODO: once viewport is more stable, use it here
	height, total := len(d.displayed), len(d.relevant)
	if total > height {
		height++ // more ... row
	}
	if d.LabelNewLine {
		height++ // label wrapped
	}
	return height
}

// snapshotState captures dropdown state so key handlers can emit semantic diffs.
func (d *dropdown) snapshotState() dropdownState {
	return dropdownState{
		prefix:   string(d.typed),
		relevant: append([]int(nil), d.relevant...),
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

func (d *dropdown) run() (int, error) {
	io, err := d.makeTermIO(d.in, d.out)
	if err != nil {
		return -1, fmt.Errorf("raw term: %w", err)
	}
	defer io.Restore() //nolint:errcheck // we can't do much about it here
	if io.Height < 3 {
		return -1, ErrNoSpace
	}
	frame := bytes.NewBuffer(make([]byte, d.height()*io.Width))
	frame.Reset()
	if d.itItems != nil {
		return d.runLazy(io, frame)
	}
	for {
		i, err := d.runRender(io, frame)
		if err != nil {
			return -1, err
		}
		if i >= 0 {
			return i, nil
		}
	}
}

// runLazy renders the dropdown while items are streamed in.
func (d *dropdown) runLazy(tio *termIO, frame *bytes.Buffer) (int, error) {
	ctx, cancel := context.WithCancel(d.Ctx)
	keys := d.readKey(ctx, tio)
	// only drain what [waitForReadableInput] can actually interrupt
	cancelable := canDrainOnCancel(tio.in)
	defer func() {
		cancel() // signal consumer to stop
		if !cancelable {
			return
		}
		for range keys {
		}
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
			keys = d.readKey(ctx, tio)
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
		nextRender, err := d.handleLazyItem(tio, frame, space, it, more)
		if err != nil {
			return lazyResult{}, err
		}
		return lazyResult{needsRender: nextRender}, nil
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

// handleLazyItem updates the dropdown for a streamed item.
func (d *dropdown) handleLazyItem(tio *termIO, frame *bytes.Buffer, space int, it itPair, more bool) (bool, error) {
	if !more {
		d.iterDone = true
		d.itItems = nil
		if len(d.Items) == 0 {
			clearErr := d.clearFrame(tio, frame, space)
			if clearErr != nil {
				return false, errors.Join(io.EOF, clearErr)
			}
			return false, ErrEmptyLazyResult
		}
		return false, nil
	}
	if it.err != nil {
		clearErr := d.clearFrame(tio, frame, space)
		if clearErr != nil {
			return false, errors.Join(it.err, clearErr)
		}
		return false, it.err
	}
	err := d.addItem(tio.Height, it.item)
	if err != nil {
		clearErr := d.clearFrame(tio, frame, space)
		if clearErr != nil {
			return false, errors.Join(err, clearErr)
		}
		return false, err
	}
	return true, nil
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
		var more *pasteTextError
		if errors.As(ev.err, &more) {
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

func (d *dropdown) runRender(io *termIO, frame *bytes.Buffer) (int, error) {
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
		return d.runMain(io, frame, space, displayed)
	}
}

func (d *dropdown) runMain(io *termIO, frame *bytes.Buffer, space, displayed int) (int, error) {
	i, err := d.pressKey(io, frame, space, displayed)
	var more *pasteTextError
	if errors.As(err, &more) {
		// Ctrl+V or CMD+V pressed
		return -1, nil
	} else if err != nil {
		frame.WriteTo(io) //nolint:errcheck // we can't do much about it here
		return -1, err
	}
	if i < 0 {
		return -1, nil
	}
	_, err = frame.WriteTo(io) // clear the screen
	if err != nil {
		return -1, fmt.Errorf("write: %w", err)
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

// addItem appends an item and refreshes derived state.
func (d *dropdown) addItem(height int, item any) error {
	d.Items = append(d.Items, item)
	d.inactive = append(d.inactive, nil)
	d.widths = append(d.widths, 0)
	d.relevant = append(d.relevant, 0)
	err := d.setItem(len(d.Items)-1, item)
	if err != nil {
		return fmt.Errorf("set item: %w", err)
	}
	d.displayed = d.relevant[:min(len(d.relevant), height/2)]
	return nil
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
	ev, ok := <-d.input
	if !ok {
		return -1, io.EOF
	}
	err := tio.clear(space, frame)
	if err != nil {
		return -1, err
	}
	i := d.applyInputEvent(tio, ev, displayed, space)
	if i >= 0 {
		_, err := frame.WriteTo(tio) // TODO: check if we can just defer it from beginning of the method
		if err != nil {
			return -1, fmt.Errorf("write: %w", err)
		}
		return i, nil
	}
	return -1, nil
}

func (d *dropdown) pressKey(tio *termIO, frame *bytes.Buffer, space, displayed int) (i int, err error) {
	if d.input != nil {
		return d.pressKeyFromInput(tio, frame, space, displayed)
	}
	key, _, readErr := tio.ReadRune()
	if readErr != nil {
		return -1, readErr
	}
	err = tio.clear(space, frame)
	if err != nil {
		return -1, err
	}
	i = d.pressKeyRune(tio, key, displayed, space)
	if i >= 0 {
		_, err := frame.WriteTo(tio) // TODO: check if we can just defer it from beginning of the method
		if err != nil {
			return -1, fmt.Errorf("write: %w", err)
		}
		return i, nil
	}
	return -1, nil
}

// clearFrame removes the last render without drawing a new frame.
func (d *dropdown) clearFrame(io *termIO, frame *bytes.Buffer, space int) error {
	if space < 1 {
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
	if d.offset > 0 && d.selected == 0 { // page up
		d.offset--
		d.displayed = d.relevant[d.offset : d.offset+displayed]
	} else if d.selected > 0 {
		d.selected--
	}
}

func (d *dropdown) pressDown(displayed int) {
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
	d.relevant = d.trie.Prefix(string(d.typed))
	d.displayed = d.relevant[:min(len(d.relevant), io.Height/2)]
	d.selected = 0
	d.offset = 0
}

// filterWith replaces the current filter text and refreshes matching rows.
func (d *dropdown) filterWith(text string, displayed, space, height int) bool {
	prevTyped := string(d.typed)
	prevRelevant := d.relevant
	d.typed = []rune(text)
	d.relevant = d.trie.Prefix(text)
	if d.OneReturn && len(d.relevant) == 1 {
		return true
	}
	if len(d.relevant) == 0 {
		d.typed = []rune(prevTyped)
		d.relevant = prevRelevant
		return false
	}
	limit := min(len(d.relevant), displayed, space)
	if len(text) < len(prevTyped) {
		limit = min(len(d.relevant), height)
	}
	d.displayed = d.relevant[:limit]
	d.selected = 0
	d.offset = 0
	return false
}

func (d *dropdown) pressAny(key rune, displayed, space int) bool {
	d.typed = append(d.typed, key)
	d.relevant = d.trie.Prefix(string(d.typed))
	if d.OneReturn && len(d.relevant) == 1 {
		return true
	}
	if len(d.relevant) == 0 {
		d.typed = d.typed[:len(d.typed)-1]
		d.relevant = d.trie.Prefix(string(d.typed))
		return false
	}
	d.displayed = d.relevant[:min(len(d.relevant), displayed, space)]
	d.selected = 0
	d.offset = 0
	return false
}

// readKey reads one keypress so lazy mode can interleave input and streamed items.
func (d *dropdown) readKey(ctx context.Context, tio *termIO) <-chan keyEvent {
	ch := make(chan keyEvent, 1)
	go func() {
		defer close(ch)
		err := waitForReadableInput(ctx, tio.in)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			ch <- keyEvent{err: err}
			return
		}
		key, _, err := tio.ReadRune()
		if ctx.Err() != nil {
			return
		}
		ch <- keyEvent{key: key, err: err}
	}()
	return ch
}
