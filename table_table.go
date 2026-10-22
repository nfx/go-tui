// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"io"
	"log/slog"
	"math"
	"reflect"
	"strings"
	"text/template"
)

// table is an alternative to text/tabwriter that supports ANSI colors and
// truncation of wide cells. It uses text/template to render each row.
// The first row is used to extract the headers from the template.
type table struct {
	w                io.Writer
	tmpl             *template.Template
	columns          []tableColumn
	metadata         structFields
	rows             [][]ansi.Text
	pending          [][]cell
	scratch          ansi.Text
	cellEnds         []int
	cellPad          int
	batchSize        int
	maxWidth         int
	maxWidthExplicit bool
	colMinWidth      int
	locked           bool
	consumed         int
	eventSink        func(tableEvent)
	ended            bool
	autoTemplate     bool
	suppressHeaders  bool
	multilineCells   bool

	// theme is captured at construction, and bound to w before options run.
	theme        *compiledTheme
	look         binding
	header, cell boundSlot

	customTemplateFuncs template.FuncMap
	templateFuncSeq     int

	columnTypeFormats map[reflect.Type]string
	columnNameFormats map[string]string
	columnTemplates   map[string]string
	includeColumns    map[string]bool
	skipColumns       map[string]bool
	columnScales      map[string]*columnScale
}

// cell is a structured table cell: its text never contains tabs or newlines,
// so field data cannot be confused with layout.
type cell struct {
	text  ansi.Text
	width int // visible width of text, tailer included
}

type tableColumn struct {
	width      int
	maxVisible int // including the truncation tailer
	meta       *fieldMetadata
}

func newTable[T any](w io.Writer, rowTmpl string, o ...opt) (*table, error) {
	return newTableFor(reflect.TypeFor[T](), w, rowTmpl, o...)
}

func newTableFor(rt reflect.Type, w io.Writer, rowTmpl string, o ...opt) (*table, error) {
	metadata, err := structFieldsForType(rt)
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	t := &table{
		w:               w,
		metadata:        metadata,
		maxWidth:        80,
		batchSize:       10,
		cellPad:         1,
		colMinWidth:     1,
		autoTemplate:    rowTmpl == "",
		templateFuncSeq: 0,
	}
	err = t.bind()
	if err != nil {
		return nil, err
	}
	err = opts(o).Apply(t)
	if err != nil {
		return nil, err
	}
	if t.autoTemplate {
		rowTmpl = t.autoRowTemplate()
		if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
			slog.Debug("table: generated template", "template", rowTmpl)
		}
	}
	for name, columnTmpl := range t.columnTemplates {
		_, err = template.New(fmt.Sprintf("column %q template", name)).Funcs(t.templateFuncs()).Parse(columnTmpl)
		if err != nil {
			return nil, fmt.Errorf("template: %w", err)
		}
	}
	t.tmpl, err = template.New("row template").Funcs(t.templateFuncs()).Parse(rowTmpl)
	if err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	err = t.headers()
	if err != nil {
		return nil, fmt.Errorf("headers: %w", err)
	}
	err = t.markCellSeparators()
	if err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}

	return t, nil
}

// bind captures the installed theme and binds it to the writer without
// probing, so that formatters, scales and slots encode for the destination.
// The layout comes from [Theme.Table]; options such as [WithMaxWidth] still
// apply on top of it.
func (t *table) bind() error {
	t.theme = defaultIO.currentTheme()
	t.look = defaultIO.bindTheme(context.Background(), t.theme, t.w, nil)
	style := t.theme.source.Table
	// a value keeps one trailing column when it is truncated or padded
	t.cellPad, t.colMinWidth = style.ColumnGap-1, style.MinColumnWidth
	slots := t.theme.templates.Table
	var errs [2]error
	t.header, errs[0] = slots.Header.bind(t.look.funcs)
	t.cell, errs[1] = slots.Cell.bind(t.look.funcs)
	return errors.Join(errs[:]...)
}

// column is the view of the column of meta.
func (*table) column(meta *fieldMetadata) columnView {
	return columnView{Header: ansi.Text(meta.header).OneLine(), Name: meta.name, Kind: meta.kind.String()}
}

// heading renders the header of a column through the Header slot.
func (t *table) heading(meta *fieldMetadata) (ansi.Text, error) {
	return t.header.render(t.column(meta))
}

// headerRow reports whether the table shows a header row.
func (t *table) headerRow() bool {
	return !t.suppressHeaders && t.header.tmpl != nil
}

func (t *table) Append(v any) error {
	cells, err := t.renderCells(v)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	err = t.addRecord(cells, t.scaleColors(v))
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	t.consumed++
	if t.consumed%t.batchSize == 0 {
		err = t.flush(false)
		if err != nil {
			return fmt.Errorf("flush: %w", err)
		}
	}

	return nil
}

// addRecord keeps each cell line and its color on the same physical row. It
// carries styles across the lines of a cell, and decorates each line.
func (t *table) addRecord(cells, colors []string) error {
	lines := make([][]string, len(cells))
	count := 1
	for i, text := range cells {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		if i == len(cells)-1 {
			// a template ending with a line break terminates the row
			text = strings.TrimSuffix(text, "\n")
		}
		text = strings.ReplaceAll(text, "\r", " ")
		text = strings.ReplaceAll(text, "\t", " ")
		if !t.multilineCells {
			text = strings.ReplaceAll(text, "\n", " ")
		}
		lines[i] = strings.Split(text, "\n")
		t.carryStyles(lines[i])
		count = max(count, len(lines[i]))
	}
	for k := range count {
		row := make([]ansi.Text, len(lines))
		var rowColors []string
		if colors != nil {
			rowColors = make([]string, len(lines))
		}
		for i := range lines {
			if k >= len(lines[i]) {
				continue
			}
			row[i] = ansi.Text(lines[i][k])
			if rowColors != nil && i < len(colors) {
				rowColors[i] = colors[i]
			}
		}
		err := t.decorate(row, rowColors)
		if err != nil {
			return err
		}
		t.addRow(row)
	}
	return nil
}

// cellSegment reports whether seg may appear in a cell line: text, SGR or a
// hyperlink. Invalid bytes become U+FFFD when the line is truncated.
func (*table) cellSegment(seg ansi.Segment) bool {
	_, sgr := seg.SGRParams()
	_, link := seg.OSC8()
	return seg.IsText() || seg.IsInvalid || seg.IsEscape && (sgr || link)
}

// cellText drops the controls and escape sequences a cell line cannot keep.
func (t *table) cellText(line ansi.Text) ansi.Text {
	var out ansi.Text
	for seg := range line.Segments() {
		if t.cellSegment(seg) {
			out = append(out, seg.Text...)
		}
	}
	return out
}

// cellOutput reports output of slot that a cell line cannot keep.
func (t *table) cellOutput(slot boundSlot, out ansi.Text) error {
	for seg := range out.Segments() {
		if !t.cellSegment(seg) {
			return fmt.Errorf("theme.Templates.%s: %q: want text, SGR and hyperlinks only", slot.tmpl.Name(), seg.Text)
		}
	}
	return nil
}

// addRow queues one physical row, keeping exactly one cell per column.
func (t *table) addRow(texts []ansi.Text) {
	row := make([]cell, len(t.columns))
	for i := range row {
		if i >= len(texts) || len(texts[i]) == 0 {
			continue
		}
		row[i] = cell{
			text:  texts[i],
			width: texts[i].TruncateVisible(math.MaxInt32, ' ').Width(),
		}
	}
	t.pending = append(t.pending, row)
}

func (t *table) emit(ev tableEvent) {
	if t.eventSink == nil {
		return
	}
	t.eventSink(ev)
}

func (t *table) flush(final bool) error {
	t.fitPending()
	if t.eventSink != nil {
		t.flushEvents(final)

		return nil
	}
	return t.flushRows()
}

func (t *table) flushEvents(final bool) {
	for _, row := range t.rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = c.String()
		}
		t.emit(tableRow{Cells: cells})
	}
	if final && !t.ended {
		t.ended = true
		t.emit(tableEnd{Rows: t.consumed})
	}
	t.rows = t.rows[:0]
}

func (t *table) flushRows() error {
	buf := &bytes.Buffer{}
	for i, row := range t.rows {
		for j, cell := range row {
			err := t.padded(buf, cell, j)
			if err != nil {
				return fmt.Errorf("padded[%d,%d]: %w", i, j, err)
			}
		}
		err := buf.WriteByte('\n')
		if err != nil {
			return fmt.Errorf("newline[%d]: %w", i, err)
		}
	}
	_, err := buf.WriteTo(t.w)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	t.rows = t.rows[:0]

	return nil
}
