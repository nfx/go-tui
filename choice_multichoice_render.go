// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"github.com/nfx/go-tui/internal/ansi"
)

// render builds a width-bounded frame from cached content and current selection state.
func (m *multichoice) render(tio *termIO, buf *bytes.Buffer) error {
	return m.renderThemed(tio, buf)
}

// multichoiceSlots are the theme slots of a multichoice bound to one destination.
type multichoiceSlots struct {
	choiceSlots
	row, answerValue, empty boundSlot
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
	m.label = m.label.BeforeNewline()
	if m.loading {
		return nil
	}
	return m.resolveDefault()
}

// projectItem caches searchable content, deferring details and non-searchable rows until drawing.
func (m *multichoice) projectItem(item any) (ansi.Text, error) {
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
func (m *multichoice) themedRow(i int, active bool) (ansi.Text, error) {
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
		if empty.Width() > 0 {
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
	help, err := themedHint(
		m.help,
		m.multichoiceKeys(m.theme.source.Symbols),
		len(m.relevant)-m.offset-len(m.displayed),
	)
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
	if err != nil || out.Width() == 0 {
		return err
	}
	return m.writeAnswer(append(out, '\n'))
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
