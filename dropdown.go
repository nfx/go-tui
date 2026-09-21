// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
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

	selected int
	offset   int

	in  io.Reader
	out io.Writer

	// TODO: special case for testing?..
	makeTermIO func(in io.Reader, out io.Writer) (*termIO, error)
}

func Confirmf(format string, a ...any) bool {
	return Confirm(fmt.Sprintf(format, a...))
}

func Confirm(action string, opts ...opt) bool {
	res, err := Dropdown(action, []string{"Yes", "No"}, opts...)
	if err != nil {
		return false
	}
	return strings.ToLower(res) == "yes"
}

func Dropdown[T any](label string, items []T, opts ...opt) (T, error) {
	var zero T
	if len(items) == 0 {
		return zero, fmt.Errorf("no items provided")
	}
	// apparently, there's no other non-reflective way around
	anyItems := make([]any, len(items))
	for i, v := range items {
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Ptr && rv.IsNil() {
			continue
		} else if rv.IsZero() {
			continue
		}
		anyItems[i] = v
	}
	i, err := DropdownIndex(label, anyItems, opts...)
	if err != nil {
		return zero, err
	}
	// we know i is valid
	return items[i], nil
}

func DropdownIndex(label string, items []any, o ...opt) (int, error) {
	d, err := newDropdown()
	if err != nil {
		return -1, err
	}
	d.Label = label
	d.Items = items
	err = opts(o).Apply(d)
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
	i := d.relevant[j]
	if !d.Hide {
		var buf bytes.Buffer
		err = d.answerTemplate.Execute(&buf, dropdownAnswer{
			Label:  label,
			Answer: items[i],
		})
		if err != nil {
			return i, fmt.Errorf("answer: %w", err)
		}
		buf.WriteTo(d.out)
	}
	return i, nil
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
			return fmt.Errorf("need a dropdown, got %v", a)
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

func newDropdown() (*dropdown, error) {
	d := &dropdown{
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
	}
	return d, nil
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

// implements [withIO]
func (d *dropdown) setReader(r io.Reader) {
	d.in = r
}

// implements [withIO]
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

// implements [withContext]
func (d *dropdown) setContext(ctx context.Context) {
	d.Ctx = ctx
}

// implements [withContext]
func (d *dropdown) getContext() context.Context {
	return d.Ctx
}

// render displays the dropdown
func (d *dropdown) render(io *termIO, buf *bytes.Buffer) error {
	// use buffer to write to io only once
	var prefix int
	var err error

	var longest int
	if len(d.displayed) == 0 {
		d.trie = newTrie()
		d.inactive = make([]bbuf, len(d.Items))
		d.widths = make([]int, len(d.Items))
		d.relevant = make([]int, len(d.Items))
		for i, item := range d.Items {
			err = d.inactiveItemTemplate.Execute(&d.inactive[i], item)
			if err != nil {
				return fmt.Errorf("inactive: %w", err)
			}
			d.trie.Add(d.inactive[i].String(), i)
			d.widths[i] = width(d.inactive[i])
			d.relevant[i] = i
			longest = max(longest, d.widths[i])
		}
		d.displayed = d.relevant[:min(len(d.relevant), io.Height/2)]
	}
	var bufMore bbuf
	total := len(d.relevant)
	height := min(total, io.Height/2)
	if total > len(d.displayed) {
		err = d.moreItemsTemplate.Execute(&bufMore, dropdownMore{
			More:  total - d.offset - height,
			Total: total,
		})
		if err != nil {
			return fmt.Errorf("more: %w", err)
		}
		longest = max(longest, width(bufMore))
	}
	var item bbuf
	var itemW int
	for i, j := range d.displayed {
		buf.WriteByte('\r') // ensure we start from the leftmost position
		if i == 0 {
			label := d.labelBuf.Bytes()
			// TODO: we still have issues when label overflows the terminal width - some terminals wrap it, some don't.
			// proper solution would be to use viewports and scroll the label as well
			prefix = width(label)
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
		} else {
			for range prefix {
				buf.WriteByte(' ')
			}
		}
		if i == d.selected {
			item = nil // clear buffer
			// only active item is re-rendered
			err = d.activeItemTemplate.Execute(&item, d.Items[j])
			if err != nil {
				return fmt.Errorf("active: %w", err)
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

type dropdownMore struct {
	More  int
	Total int
}

func (d *dropdown) height(io *termIO) int {
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
	defer io.Restore()
	if io.Height < 3 {
		return -1, ErrNoSpace
	}
	frame := bytes.NewBuffer(make([]byte, d.height(io)*io.Width))
	frame.Reset()
	var typed []rune
	for {
		err = d.render(io, frame)
		if err != nil {
			return -1, fmt.Errorf("render: %w", err)
		}
		frame.WriteTo(io)
		space := d.height(io)
		displayed := len(d.displayed)
		select {
		case <-d.Ctx.Done():
			io.clear(space, frame)
			frame.WriteTo(io)
			return -1, d.Ctx.Err()
		default:
			key, err := io.ReadRune()
			io.clear(space, frame)
			if err != nil {
				if errors.Is(err, ErrUnknownRune) {
					continue
				}
				frame.WriteTo(io) // clear the screen
				// Ctrl+C or Ctrl+D
				return -1, err
			}
			switch key {
			case keyEnter:
				frame.WriteTo(io)
				return d.offset + d.selected, nil
			case '↑':
				if d.offset > 0 && d.selected == 0 { // page up
					d.offset--
					d.displayed = d.relevant[d.offset : d.offset+displayed]
				} else if d.selected > 0 {
					d.selected--
				}
			case '↓':
				if d.offset+displayed < len(d.relevant) { // page down
					d.offset++
					d.displayed = d.relevant[d.offset : d.offset+displayed]
				} else if d.selected < displayed-1 {
					d.selected++
				}
			case 0x7f: // backspace
				if len(typed) > 0 {
					typed = typed[:len(typed)-1]
					d.relevant = d.trie.Prefix(string(typed))
					d.displayed = d.relevant[:min(len(d.relevant), io.Height/2)]
					d.selected = 0
					d.offset = 0
				}
			default:
				typed = append(typed, key)
				d.relevant = d.trie.Prefix(string(typed))
				if d.OneReturn && len(d.relevant) == 1 {
					return 0, nil
				}
				if len(d.relevant) == 0 {
					typed = typed[:len(typed)-1]
					d.relevant = d.trie.Prefix(string(typed))
				} else {
					d.displayed = d.relevant[:min(len(d.relevant), displayed, space)]
					d.selected = 0
					d.offset = 0
				}
			}
		}
	}
}
