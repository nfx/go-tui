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
	"text/template"
)

type multichoice struct {
	Ctx       context.Context
	Label     string
	Items     []any
	trie      *trie
	relevant  []int
	displayed []int
	selected  []bool
	active    int

	inactive []bbuf
	widths   []int
	Default  []any

	LabelTemplate     string
	labelBuf          bytes.Buffer
	ItemTemplate      string
	itemTemplate      *template.Template
	MoreItemsTemplate string
	moreItemsTemplate *template.Template

	offset int

	in   io.Reader
	out  io.Writer
	init bool

	makeTermIO func(in io.Reader, out io.Writer) (*termIO, error)
}

var DefaultMultichoiceItemTemplate = `[{{if .Selected}}x{{else}} {{end}}] {{.Value}}`

func newMultichoice() *multichoice {
	return &multichoice{
		in:                os.Stdin,
		out:               os.Stderr,
		Ctx:               context.Background(),
		Label:             "Select an item",
		LabelTemplate:     DefaultLabelTemplate,
		ItemTemplate:      DefaultMultichoiceItemTemplate,
		MoreItemsTemplate: DefaultMoreItemsTemplate,
		makeTermIO:        makeTermIO,
	}
}

// render displays the dropdown.
//
//nolint:cyclop,funlen,gocognit,ineffassign,nestif,staticcheck // TODO: unfinished
func (m *multichoice) render(io *termIO, buf *viewport) error {
	// use buffer to write to io only once
	var prefix int
	var err error

	var longest int
	if len(m.displayed) == 0 {
		m.trie = newTrie()
		m.inactive = make([]bbuf, len(m.Items))
		m.widths = make([]int, len(m.Items))
		m.relevant = make([]int, len(m.Items))
		for i, item := range m.Items {
			err = m.itemTemplate.Execute(&m.inactive[i], item)
			if err != nil {
				return fmt.Errorf("inactive: %w", err)
			}
			m.trie.Add(m.inactive[i].String(), i)
			m.widths[i] = width(m.inactive[i])
			m.relevant[i] = i
			longest = max(longest, m.widths[i])
		}
		m.displayed = m.relevant[:min(len(m.relevant), io.Height/2)]
	}
	var bufMore bbuf
	total := len(m.relevant)
	height := min(total, io.Height/2)
	if total > len(m.displayed) {
		err = m.moreItemsTemplate.Execute(&bufMore, dropdownMore{
			More:  total - m.offset - height,
			Total: total,
		})
		if err != nil {
			return fmt.Errorf("more: %w", err)
		}
		longest = max(longest, width(bufMore))
	}
	var item bbuf
	var itemW int
	err = buf.WriteByte('\r') // ensure we start from the leftmost position
	if err != nil {
		return fmt.Errorf("rewind: %w", err)
	}
	label := m.labelBuf.Bytes()
	// TODO: we still have issues when label overflows the terminal width - some terminals wrap it, some don't.
	// proper solution would be to use viewports and scroll the label as well
	prefix = width(label)
	if prefix > io.Width {
		label = truncateVisible(label, io.Width-1, ' ')
	}
	_, err = buf.Write(label)
	if err != nil {
		return fmt.Errorf("label: %w", err)
	}
	err = buf.WriteByte('\n')
	if err != nil {
		return fmt.Errorf("newline: %w", err)
	}
	err = buf.WriteByte('\r')
	if err != nil {
		return fmt.Errorf("rewind: %w", err)
	}
	for i, j := range m.displayed {
		if i == m.active {
			item = nil // clear buffer
			// only active item is re-rendered
			err = m.itemTemplate.Execute(&item, m.Items[j])
			if err != nil {
				return fmt.Errorf("active: %w", err)
			}
			itemW = width(item)
		} else {
			item = m.inactive[j]
			itemW = m.widths[j]
		}
		if itemW > io.Width {
			// this may fail if active item is wider than the terminal, but we can solve this later
			item = truncateVisible(item, io.Width-1, '\n')
		}
		_, err = buf.Write(item)
		if err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}
	if total > len(m.displayed) {
		err = buf.WriteByte('\r') // always display a line to avoid flickering
		if err != nil {
			return fmt.Errorf("rewind: %w", err)
		}
		if m.offset+height < total {
			_, err = buf.Write(bufMore)
			if err != nil {
				return fmt.Errorf("more: %w", err)
			}
		}
		err = buf.WriteByte('\n')
		if err != nil {
			return fmt.Errorf("newline: %w", err)
		}
	}
	err = buf.WriteByte('\r')
	if err != nil {
		return fmt.Errorf("rewind: %w", err)
	}

	return nil
}

type multichoiceItem struct {
	Value    any
	Selected bool
	Active   bool
}

//nolint:cyclop,errcheck,funlen,gocognit // TODO: unfinished
func (m *multichoice) run() error {
	io, err := m.makeTermIO(m.in, m.out)
	if err != nil {
		return fmt.Errorf("raw term: %w", err)
	}
	defer io.Restore() //nolint:errcheck // best effort
	if io.Height < 3 {
		return ErrNoSpace
	}
	// TODO: unfinished
	frame := initViewport(m.Ctx, make(chan viewportChanged), io.Width, io.Height)
	var typed []rune
	for {
		err = m.render(io, frame)
		if err != nil {
			return fmt.Errorf("render: %w", err)
		}
		frame.WriteTo(io)
		displayed := len(m.displayed)
		space := frame.numLines()
		frame.height = space
		select {
		case <-m.Ctx.Done():
			io.clear(space, frame)
			frame.WriteTo(io)

			return m.Ctx.Err()
		default:
			key, _, err := io.ReadRune()
			io.clear(space, frame)
			if err != nil {
				var more *pasteTextError
				if errors.As(err, &more) {
					// Ctrl+V or CMD+V pressed
					continue
				}
				frame.WriteTo(io) // clear the screen
				// Ctrl+C or Ctrl+D
				return err
			}
			switch key {
			case keyEnter:
				frame.WriteTo(io)

				return nil
			case '↑':
				if m.offset > 0 && m.active == 0 { // page up
					m.offset--
					m.displayed = m.relevant[m.offset : m.offset+displayed]
				} else if m.active > 0 {
					m.active--
				}
			case '↓':
				if m.offset+displayed < len(m.relevant) { // page down
					m.offset++
					m.displayed = m.relevant[m.offset : m.offset+displayed]
				} else if m.active < displayed-1 {
					m.active++
				}
			case ' ':
				m.selected[m.relevant[m.offset+m.active]] = !m.selected[m.relevant[m.offset+m.active]]
			case 0x7f: // backspace
				if len(typed) > 0 {
					typed = typed[:len(typed)-1]
					m.relevant = m.trie.Prefix(string(typed))
					m.displayed = m.relevant[:min(len(m.relevant), io.Height/2)]
					m.active = 0
					m.offset = 0
				}
			default:
				typed = append(typed, key)
				m.relevant = m.trie.Prefix(string(typed))
				if len(m.relevant) == 0 {
					typed = typed[:len(typed)-1]
					m.relevant = m.trie.Prefix(string(typed))
				} else {
					m.displayed = m.relevant[:min(len(m.relevant), displayed, space)]
					m.active = 0
					m.offset = 0
				}
			}
		}
	}
}
