// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"text/template"
)

type factCell struct {
	title      Text
	value      Text
	width      int
	alignRight bool
}

// facts present one record as keys over values, laid out and rendered as a
// table: a key is a column header and a value a cell.
type facts struct {
	*table
}

// minColumn is the narrowest fact column: as in tables, the minimum visible
// text plus the truncation tailer, then the gap.
func (f *facts) minColumn() int {
	return f.colMinWidth + 1 + f.cellPad
}

func (f *facts) factsMaxWidth(w io.Writer) int {
	maxWidth := f.maxWidth
	ttyWidth, ok := f.readTTYWidth(w)
	if ok {
		maxWidth = ttyWidth
		if f.maxWidthExplicit && f.maxWidth < maxWidth {
			maxWidth = f.maxWidth
		}
	}
	maxWidth = max(maxWidth, f.minColumn())
	return maxWidth
}

func (*facts) readTTYWidth(w io.Writer) (int, bool) {
	if out, ok := w.(descriptor); ok {
		width, _, err := termGetSize(int(out.Fd()))
		if err == nil && width > 0 {
			return width, true
		}
	}
	width, _, err := termGetSize(int(os.Stderr.Fd()))
	if err == nil && width > 0 {
		return width, true
	}
	return 0, false
}

func (f *facts) renderFacts(facts any, maxWidth int) error {
	cells, err := f.renderFactCells(facts)
	if err != nil {
		return err
	}
	if len(cells) == 0 {
		return nil
	}
	cols := f.maxFactsColumns(cells, maxWidth)
	rows, colWidths := f.packFactRows(cells, cols, maxWidth)
	for i, row := range rows {
		err = f.renderFactRow(row, colWidths)
		if err != nil {
			return err
		}
		if i < len(rows)-1 {
			err = f.writeRowGap()
			if err != nil {
				return err
			}
		}
	}
	err = f.writeRowGap()
	if err != nil {
		return err
	}
	return nil
}

func (f *facts) writeRowGap() error {
	_, err := f.w.Write([]byte("\n"))
	if err != nil {
		return fmt.Errorf("row gap: %w", err)
	}
	return nil
}

// renderFactCells renders fact values and measures their terminal widths.
func (f *facts) renderFactCells(facts any) ([]factCell, error) {
	cells := make([]factCell, len(f.metadata))
	funcs := f.templateFuncs()
	for i, meta := range f.metadata {
		columnTmpl := f.columnRenderTemplate(meta)
		tmpl, err := template.New("fact").Funcs(funcs).Parse(columnTmpl)
		if err != nil {
			return nil, fmt.Errorf("column %q template: %w", meta.name, err)
		}
		var buf Text
		err = tmpl.Execute(&buf, facts)
		if err != nil {
			return nil, fmt.Errorf("column %q render: %w", meta.name, err)
		}
		header, value, err := f.fact(meta, buf)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", meta.name, err)
		}
		cellWidth := max(
			max(header.Width(), value.Width())+f.cellPad+1,
			f.minColumn())
		cells[i] = factCell{
			title:      header,
			value:      value,
			width:      cellWidth,
			alignRight: meta.alignRight,
		}
	}
	return cells, nil
}

// fact presents a key and its formatted value through the Header and Cell
// slots, the latter over the first line of the value. An empty value stays
// empty, as in tables.
func (f *facts) fact(meta *fieldMetadata, out Text) (key, value Text, err error) {
	value = Text(bytes.ReplaceAll(bytes.TrimRight(out, "\r\n"), []byte{'\t'}, []byte{' '}))
	view := cellView{columnView: f.column(meta), Value: f.cellText(value.BeforeNewline()).String()}
	key, err = f.header.render(view.columnView)
	if err != nil || view.Value == "" {
		return key, nil, err
	}
	value, err = f.cell.execute(view)
	if err == nil {
		err = f.cellOutput(f.cell, value)
	}
	return key, value, err
}

func (f *facts) maxFactsColumns(cells []factCell, maxWidth int) int {
	maxCell := f.minColumn()
	for _, cell := range cells {
		w := min(cell.width, maxWidth)
		if w > maxCell {
			maxCell = w
		}
	}
	return min(max(maxWidth/maxCell, 1), len(cells))
}

func (f *facts) packFactRows(cells []factCell, cols, maxWidth int) ([][]factCell, []int) {
	rows := make([][]factCell, 0, len(cells))
	colWidths := make([]int, cols)
	for i, cell := range cells {
		if cell.width > maxWidth {
			cell.width = maxWidth
		}
		col := i % cols
		if i%cols == 0 {
			rows = append(rows, []factCell{})
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], cell)
		if cell.width > colWidths[col] {
			colWidths[col] = cell.width
		}
	}
	for i := range colWidths {
		colWidths[i] = max(colWidths[i], f.minColumn())
	}
	return rows, colWidths
}

// renderFactRow clips a fact row to its allocated column widths.
func (f *facts) renderFactRow(row []factCell, colWidths []int) error {
	headers := make([]Text, len(row))
	values := make([]Text, len(row))
	keys := false
	f.columns = make([]tableColumn, len(row))
	for i, cell := range row {
		maxLen := colWidths[i] - f.cellPad
		headers[i] = cell.title.TruncateVisible(maxLen, ' ')
		keys = keys || len(cell.title) > 0
		values[i] = cell.value.TruncateVisible(maxLen, ' ')
		f.columns[i] = tableColumn{
			width: colWidths[i],
			meta:  &fieldMetadata{alignRight: cell.alignRight},
		}
	}
	if keys {
		f.rows = append(f.rows, headers)
	}
	f.rows = append(f.rows, values)
	return f.flush(false)
}
