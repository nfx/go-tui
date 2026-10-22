// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
)

// bind installs destination-bound helpers and choice slots.
func (d *dropdown) bind(look binding) error {
	d.look = look
	err := d.rebind(look.funcs)
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

// renderChrome renders the label row and binds the hint once per run.
func (d *dropdown) renderChrome() (err error) {
	d.label, d.hint, err = d.family.chrome(d.look.funcs, d.Label)
	return err
}

// themedItem caches both item rows and returns projected content for filtering.
// The row slots get the renderer's focus mark.
func (d *dropdown) themedItem(i int, item any) (Text, error) {
	content, err := d.projectContent(d.slot.value, item)
	if err != nil {
		return nil, err
	}
	details, err := d.projectDetails(d.slot.details, item)
	if err != nil {
		return nil, err
	}
	view := choiceView{Value: content.String(), Details: details.String(), Index: i, Active: true}
	d.active[i], err = d.themedRow(d.slot.active, view)
	if err != nil {
		return nil, err
	}
	view.Active = false
	d.inactive[i], err = d.themedRow(d.slot.inactive, view)
	if err != nil {
		return nil, err
	}
	d.content[i] = content
	d.search[i] = d.plainText(content)
	return content, nil
}

// themedRow renders one state of an item on a single line.
func (d *dropdown) themedRow(slot boundSlot, view choiceView) (Text, error) {
	out, err := slot.render(view)
	if err != nil {
		return nil, err
	}
	mark := Text("  ")
	if view.Active {
		mark = append(d.look.style(d.look.roles.accent, Text(d.theme.source.Symbols.Focus)), ' ')
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
	var content Text
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
	if err != nil || out.Width() == 0 {
		return err
	}
	_, err = d.out.Write(append(out, '\n'))
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
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
	d.trie = NewTrie()
	d.active = make([]Text, len(d.Items))
	d.activeWidths = make([]int, len(d.Items))
	d.inactive = make([]Text, len(d.Items))
	d.widths = make([]int, len(d.Items))
	d.content = make([]Text, len(d.Items))
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

// renderStates caches the active and inactive rows of item at i and returns
// the text that filtering matches.
func (d *dropdown) renderStates(i int, item any) (Text, error) {
	return d.themedItem(i, item)
}

// render draws the label, the visible rows and the hint, led by the count of
// rows below them, in the geometry of the theme. Every row ends with a newline,
// so that [termIO.clear] erases the frame.
func (d *dropdown) render(io *termIO, buf *bytes.Buffer) error {
	io.refreshSize()
	_, err := d.renderInit(io)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	d.clampDisplay(io.Height)
	hint, err := d.themedHint()
	if err != nil {
		return err
	}
	// the newline after the last row moves the cursor to a row of its own,
	// which must stay on the screen, or the terminal scrolls the label away
	g := d.look.geometry(io.Width, io.Height-1)
	rows := make([]frameRow, 0, len(d.displayed)+4)
	// Inline, the label leads the first row and indents the others, unless
	// the widest row does not fit after it.
	var lead, indent Text
	if d.label.Width() > 0 {
		// the renderer owns the spacing after the label
		lead = append(append(Text{}, d.label...), ' ')
	}
	if g.inline && lead.Width()+d.longest <= g.columns(nil) {
		indent = Text(strings.Repeat(" ", lead.Width()))
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
		rows = append(rows, frameRow{content: append(append(Text{}, prefix...), row...), guided: true})
	}
	// on a short terminal, the hint with the count goes first, then the label
	rows = append(rows, frameRow{content: hint, guided: true, drop: 2})
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

// themedHint renders the key hints, led by the count of the rows below the
// visible ones, if any.
func (d *dropdown) themedHint() (Text, error) {
	return themedHint(d.hint, d.dropdownKeys(d.theme.source.Symbols), len(d.relevant)-d.offset-len(d.displayed))
}
