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
	"sort"
	"strings"
	"text/template"
)

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

	in  io.Reader
	out io.Writer

	// TODO: special case for testing?..
	makeTermIO func(in io.Reader, out io.Writer) (*termIO, error)
}

type itPair struct {
	item any
	err  error
}

func Confirmf(format string, a ...any) bool {
	return Confirm(fmt.Sprintf(format, a...))
}

func Confirm(action string, opts ...opt) bool {
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
	d.itItems = make(chan itPair)
	go func() {
		defer close(d.itItems)
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
	i, err := d.dropdownIndex(o...)
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
	if d.OneReturn && len(d.Items) == 1 {
		return 0, nil
	}
	err = d.parseTemplates()
	if err != nil {
		return -1, fmt.Errorf("templates: %w", err)
	}
	j, err := d.run()
	if err != nil {
		return -1, err
	}

	return d.relevant[j], nil
}

var DefaultLabelTemplate = `{{ "?" | green }} {{ . | bold }}`
var DefaultDropdownActiveItemTemplate = `{{ cyan "→ " . }}`
var DefaultDropdownInactiveItemTemplate = `{{ dim "→ " . }}`
var DefaultMoreItemsTemplate = ` {{ dim "↓ " .More " more … (" .Total " total)" | italic }}`
var DefaultAnswerTemplate = `{{ dim "✔ " .Label " …" }} {{ .Answer | bold }}`

type dropdownAnswer struct {
	Label  string
	Answer any
}

func dropdownOpt(o func(d *dropdown) error) opt {
	return func(a any) error {
		// check if a is any dropdown
		d, ok := a.(*dropdown)
		if !ok {
			return fmt.Errorf("%w: need a dropdown, got %v", ErrInvalidState, a)
		}

		return o(d)
	}
}

func WithOneReturn() opt {
	return dropdownOpt(func(d *dropdown) error {
		d.OneReturn = true

		return nil
	})
}

func WithHide() opt {
	return dropdownOpt(func(d *dropdown) error {
		d.Hide = true

		return nil
	})
}

func WithLabelTemplate(tmpl string) opt {
	return dropdownOpt(func(d *dropdown) error {
		d.LabelTemplate = tmpl

		return nil
	})
}

func WithActiveItemTemplate(tmpl string) opt {
	return dropdownOpt(func(d *dropdown) error {
		d.ActiveItemTemplate = tmpl

		return nil
	})
}

func WithInactiveItemTemplate(tmpl string) opt {
	return dropdownOpt(func(d *dropdown) error {
		d.InactiveItemTemplate = tmpl

		return nil
	})
}

func WithMoreItemsTemplate(tmpl string) opt {
	return dropdownOpt(func(d *dropdown) error {
		d.MoreItemsTemplate = tmpl

		return nil
	})
}

func WithAnswerTemplate(tmpl string) opt {
	return dropdownOpt(func(d *dropdown) error {
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
		AnswerTemplate:       DefaultAnswerTemplate,
		IterBatchSize:        10,
	}
}

func (d *dropdown) parseTemplates() error {
	tmpl := template.New("dropdown").Funcs(colorFns)
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
	d.displayed = d.relevant[:min(len(d.relevant), io.Height/2)]

	return longest, nil
}

func (d *dropdown) setItem(i int, item any) error {
	err := d.inactiveItemTemplate.Execute(&d.inactive[i], item)
	if err != nil {
		return fmt.Errorf("inactive: %w", err)
	}
	d.trie.Add(d.inactive[i].String(), i)
	d.widths[i] = width(d.inactive[i])
	d.relevant[i] = i

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

func (d *dropdown) runRender(io *termIO, frame *bytes.Buffer) (int, error) {
	err := d.render(io, frame)
	if err != nil {
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
	if d.itItems != nil && len(d.Items) < io.Height && !d.iterDone {
		return -1, nil
	}
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

// this method is still work in progress.
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
	d.Items = append(d.Items, it.item)
	d.inactive = append(d.inactive, nil)
	d.widths = append(d.widths, 0)
	d.relevant = append(d.relevant, 0)
	err := d.setItem(len(d.Items)-1, it.item)
	if err != nil {
		return fmt.Errorf("set item: %w", err)
	}
	d.displayed = d.relevant[:min(len(d.relevant), io.Height/2)]
	// if len(d.relevant) > displayed {
	// 	space += 2
	// }
	var errs []error
	err = io.clear(io.Height, frame)
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

func (d *dropdown) pressKey(io *termIO, frame *bytes.Buffer, space, displayed int) (i int, err error) {
	key, _, err := io.ReadRune()
	if err != nil {
		return -1, err
	}
	err = io.clear(space, frame)
	if err != nil {
		return -1, err
	}
	switch key {
	case keyEnter:
		_, err := frame.WriteTo(io) // TODO: check if we can just defer it from beginning of the method
		if err != nil {
			return -1, fmt.Errorf("write: %w", err)
		}

		return d.offset + d.selected, nil
	case '↑':
		d.pressUp(displayed)
	case '↓':
		d.pressDown(displayed)
	case 0x7f: // backspace
		d.pressBackspace(io)
	default:
		done := d.pressAny(key, displayed, space)
		if done {
			return 0, nil
		}
	}

	return -1, nil
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
