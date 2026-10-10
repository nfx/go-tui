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
	"log/slog"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
	"time"
)

type tableEvent interface {
	isTableEvent()
}

type tableEventMarker struct{}

func (tableEventMarker) isTableEvent() {}

type tableBegin struct {
	tableEventMarker
	Columns []tableColumnInfo
}

type tableColumnInfo struct {
	Header string
	Kind   string
}

type tableRow struct {
	tableEventMarker
	Cells []string
}

type tableEnd struct {
	tableEventMarker
	Rows int
}

func TableIter[T any](w io.Writer, rowTmpl string, iterator iter.Seq2[T, error], o ...opt) error {
	t, err := newTable[T](w, rowTmpl, o...)
	if err != nil {
		return fmt.Errorf("table: %w", err)
	}
	for v, err := range iterator {
		if err != nil {
			return fmt.Errorf("row %d: fetch: %w", t.consumed, err)
		}
		err = t.Append(v)
		if err != nil {
			return fmt.Errorf("row %d: append: %w", t.consumed, err)
		}
	}

	return t.flush(true)
}

func Table[T any](w io.Writer, rowTmpl string, iterator []T, o ...opt) error {
	t, err := newTable[T](w, rowTmpl, o...)
	if err != nil {
		return fmt.Errorf("table: %w", err)
	}
	for _, v := range iterator {
		err = t.Append(v)
		if err != nil {
			return fmt.Errorf("row %d: append: %w", t.consumed, err)
		}
	}

	return t.flush(true)
}

func TableAuto[T any](w io.Writer, iterator []T, o ...opt) error {
	t, err := newTable[T](w, "", o...)
	if err != nil {
		return fmt.Errorf("table: %w", err)
	}
	for _, v := range iterator {
		err = t.Append(v)
		if err != nil {
			return fmt.Errorf("row %d: append: %w", t.consumed, err)
		}
	}

	return t.flush(true)
}

func Facts[T any](w io.Writer, data T, o ...opt) error {
	options := make([]opt, 0, len(o)+1)
	options = append(options, opT(func(t *table) error {
		t.suppressHeaders = true
		return nil
	}))
	options = append(options, o...)

	rt := reflect.TypeFor[T]()
	if rt.Kind() == reflect.Interface {
		// interface-typed input has no static schema; use the supplied value
		if any(data) == nil {
			return errors.New("facts: nil value")
		}
		rt = reflect.TypeOf(data)
	}
	t, err := newTableFor(rt, w, "", options...)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	f := &facts{table: t}
	maxWidth := f.factsMaxWidth(w)
	err = f.renderFacts(data, maxWidth)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

func WithColumnTypeFormat[T any](fn func(v T) string) opt {
	return opT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		if fn == nil {
			return errors.New("column type formatter cannot be nil")
		}
		valueType := reflect.TypeFor[T]()
		funcName := t.registerTemplateFunc("tableColumnTypeFormat", fn)
		t.ensureAutoTemplateMaps()
		t.columnTypeFormats[valueType] = funcName
		return nil
	})
}

func WithColumnFormat[T any](name string, fn func(v T) string) opt {
	return opT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		if fn == nil {
			return errors.New("column formatter cannot be nil")
		}
		meta, err := t.columnMetadata(name)
		if err != nil {
			return err
		}
		valueType := reflect.TypeFor[T]()
		if meta.typ != valueType {
			return fmt.Errorf("column %q has type %s, formatter expects %s", name, meta.typ, valueType)
		}
		funcName := t.registerTemplateFunc("tableColumnNameFormat", fn)
		t.ensureAutoTemplateMaps()
		t.columnNameFormats[name] = funcName
		return nil
	})
}

func WithColumnTemplate(name, columnTmpl string) opt {
	return opT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		_, err := t.columnMetadata(name)
		if err != nil {
			return err
		}
		if strings.TrimSpace(columnTmpl) == "" {
			return fmt.Errorf("column %q template cannot be empty", name)
		}
		t.ensureAutoTemplateMaps()
		t.columnTemplates[name] = columnTmpl
		return nil
	})
}

func WithIncludeColumns(names ...string) opt {
	return opT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		set, err := t.columnSet(names)
		if err != nil {
			return err
		}
		for name := range set {
			skipped, exists := t.skipColumns[name]
			if exists && skipped {
				return fmt.Errorf("column %q cannot be included and skipped", name)
			}
		}
		t.ensureAutoTemplateMaps()
		t.includeColumns = set
		return nil
	})
}

func WithSkipColumns(names ...string) opt {
	return opT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		set, err := t.columnSet(names)
		if err != nil {
			return err
		}
		for name := range set {
			included, exists := t.includeColumns[name]
			if exists && included {
				return fmt.Errorf("column %q cannot be included and skipped", name)
			}
		}
		t.ensureAutoTemplateMaps()
		t.skipColumns = set
		return nil
	})
}

// WithMaxWidth caps the total rendered row width and cell padding included.
func WithMaxWidth(chars int) opt {
	return opT(func(t *table) error {
		if chars <= 0 {
			return fmt.Errorf("%w: max width must be greater than 0", ErrInvalidState)
		}
		t.maxWidth = chars
		t.maxWidthExplicit = true
		return nil
	})
}

// WithMultilineCells renders line breaks inside cells as aligned physical rows.
// Without it, line breaks inside cells are rendered as spaces.
func WithMultilineCells() opt {
	return opT(func(t *table) error {
		t.multilineCells = true
		return nil
	})
}

func WithFloat64AsPercent() opt {
	return WithColumnTypeFormat(func(v float64) string {
		return percentString(v)
	})
}

func WithFloat64AsPercentPositiveColored() opt {
	return WithColumnTypeFormat(func(v float64) string {
		out := percentString(v)
		if v > 0 {
			return green + out + reset
		}
		if v < 0 {
			return red + out + reset
		}
		return out
	})
}

// WithColumnGreenRedScale colors an auto-template column using a 5-step
// scale from green (lowest ranked values) to red (highest ranked values),
// based on unique seen-so-far values. For TableIter, previously rendered
// rows are not recalculated.
func WithColumnGreenRedScale(name string) opt {
	return opT(func(t *table) error {
		return t.setColumnScale(name, greenRedScalePalette[:])
	})
}

// WithColumnRedGreenScale colors an auto-template column using a 5-step
// scale from red (lowest ranked values) to green (highest ranked values),
// based on unique seen-so-far values. For TableIter, previously rendered
// rows are not recalculated.
func WithColumnRedGreenScale(name string) opt {
	return opT(func(t *table) error {
		return t.setColumnScale(name, redGreenScalePalette[:])
	})
}

// percentString scales values to percentages and truncates toward zero.
func percentString(v float64) string {
	scaled := v * 10000
	scaled = math.Nextafter(scaled, math.Copysign(math.Inf(1), scaled))
	value := math.Trunc(scaled) / 100
	if value == 0 {
		return "0%"
	}
	if math.Abs(value) >= 10 {
		return fmt.Sprintf("%.0f%%", math.Trunc(value))
	}
	out := fmt.Sprintf("%.2f%%", value)
	return strings.Replace(out, ".00%", "%", 1)
}

var (
	timeType         = reflect.TypeFor[time.Time]()
	timeDurationType = reflect.TypeFor[time.Duration]()
	fmtStringerType  = reflect.TypeFor[fmt.Stringer]()
)

var greenRedScalePalette = [...]string{brightGreen, green, yellow, red, brightRed}

var redGreenScalePalette = [...]string{brightRed, red, yellow, green, brightGreen}

type greenRedScalarFn func(v any) (float64, bool)

type columnScale struct {
	toScalar    greenRedScalarFn
	palette     []string
	roundFloats bool
	unique      []float64
}

func (s *columnScale) color(v any) string {
	scalar, ok := s.scalar(v)
	if !ok {
		return ""
	}
	scalar = s.normalize(scalar)
	rank, total := s.observeRank(scalar)
	if total < 2 {
		return ""
	}
	if len(s.palette) == 0 {
		return ""
	}
	return s.palette[s.bucketIndex(rank, total)]
}

func (s *columnScale) scalar(v any) (float64, bool) {
	if s.toScalar == nil {
		return 0, false
	}
	scalar, ok := s.toScalar(v)
	if !ok || math.IsNaN(scalar) || math.IsInf(scalar, 0) {
		return 0, false
	}
	return scalar, true
}

func (s *columnScale) normalize(scalar float64) float64 {
	if !s.roundFloats {
		return scalar
	}
	const factor = 10000.0
	return math.Round(scalar*factor) / factor
}

func (s *columnScale) observeRank(scalar float64) (rank, total int) {
	idx := sort.SearchFloat64s(s.unique, scalar)
	if idx >= len(s.unique) || s.unique[idx] != scalar {
		s.unique = append(s.unique, 0)
		copy(s.unique[idx+1:], s.unique[idx:])
		s.unique[idx] = scalar
	}
	return idx, len(s.unique)
}

func (s *columnScale) bucketIndex(rank, total int) int {
	if total < 2 {
		return 0
	}
	if len(s.palette) < 2 {
		return 0
	}
	numerator := rank * (len(s.palette) - 1)
	denominator := total - 1
	idx := int(math.Round(float64(numerator) / float64(denominator)))
	if idx < 0 {
		return 0
	}
	if idx >= len(s.palette) {
		return len(s.palette) - 1
	}
	return idx
}

// table is an alternative to text/tabwriter that supports ANSI colors and
// truncation of wide cells. It uses text/template to render each row.
// The first row is used to extract the headers from the template.
type table struct {
	w                io.Writer
	tmpl             *template.Template
	columns          []tableColumn
	metadata         structFields
	rows             [][]text
	pending          [][]cell
	scratch          text
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
	text  text
	width int    // visible width of text, tailer included
	color string // optional ANSI prefix, closed by reset when rendered
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
	t.tmpl, err = template.New("row").Funcs(t.templateFuncs()).Parse(rowTmpl)
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

type factCell struct {
	title      text
	value      text
	width      int
	alignRight bool
}

type facts struct {
	*table
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
	if maxWidth < f.colMinWidth+f.cellPad {
		maxWidth = f.colMinWidth + f.cellPad
	}
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
		var buf text
		err = tmpl.Execute(&buf, facts)
		if err != nil {
			return nil, fmt.Errorf("column %q render: %w", meta.name, err)
		}
		header := mkBold(strings.ReplaceAll(meta.header, "\t", " "))
		value := text(bytes.ReplaceAll(bytes.TrimRight(buf, "\r\n"), []byte{'\t'}, []byte{' '}))
		cellWidth := max(
			max(header.width(), value.width())+f.cellPad+1,
			f.colMinWidth+f.cellPad)
		cells[i] = factCell{
			title:      header,
			value:      value,
			width:      cellWidth,
			alignRight: meta.alignRight,
		}
	}
	return cells, nil
}

func (f *facts) maxFactsColumns(cells []factCell, maxWidth int) int {
	maxCell := f.colMinWidth + f.cellPad
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
		if colWidths[i] < f.colMinWidth+f.cellPad {
			colWidths[i] = f.colMinWidth + f.cellPad
		}
	}
	return rows, colWidths
}

// renderFactRow clips a fact row to its allocated column widths.
func (f *facts) renderFactRow(row []factCell, colWidths []int) error {
	headers := make([]text, len(row))
	values := make([]text, len(row))
	f.columns = make([]tableColumn, len(row))
	for i, cell := range row {
		maxLen := max(colWidths[i]-f.cellPad, f.colMinWidth)
		headers[i] = cell.title.truncateVisible(maxLen, ' ')
		values[i] = cell.value.truncateVisible(maxLen, ' ')
		f.columns[i] = tableColumn{
			width: colWidths[i],
			meta:  &fieldMetadata{alignRight: cell.alignRight},
		}
	}
	f.rows = append(f.rows, headers, values)
	return f.flush(false)
}

func (t *table) Append(v any) error {
	cells, err := t.renderCells(v)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	t.addRecord(cells, t.scaleColors(v))
	t.consumed++
	if t.consumed%t.batchSize == 0 {
		err = t.flush(false)
		if err != nil {
			return fmt.Errorf("flush: %w", err)
		}
	}

	return nil
}

func (t *table) cellSeparator() string {
	t.cellEnds = append(t.cellEnds, len(t.scratch))
	return ""
}

// markCellSeparators replaces every tab in template text with a __cellSep call,
// so only tabs authored in the template separate cells.
func (t *table) markCellSeparators() error {
	sepTmpl, err := template.New("sep").Funcs(t.templateFuncs()).Parse("{{__cellSep}}")
	if err != nil {
		return err
	}
	sep := sepTmpl.Root.Nodes[0]
	seen := map[*parse.Tree]bool{}
	for _, tmpl := range t.tmpl.Templates() {
		if tmpl.Tree == nil || seen[tmpl.Tree] {
			continue
		}
		seen[tmpl.Tree] = true
		t.splitTabs(tmpl.Root, sep)
	}
	return nil
}

func (t *table) splitTabs(list *parse.ListNode, sep parse.Node) {
	if list == nil {
		return
	}
	nodes := make([]parse.Node, 0, len(list.Nodes))
	for _, n := range list.Nodes {
		switch x := n.(type) {
		case *parse.TextNode:
			nodes = append(nodes, t.splitTextNode(x, sep)...)
			continue
		case *parse.IfNode:
			t.splitTabs(x.List, sep)
			t.splitTabs(x.ElseList, sep)
		case *parse.RangeNode:
			t.splitTabs(x.List, sep)
			t.splitTabs(x.ElseList, sep)
		case *parse.WithNode:
			t.splitTabs(x.List, sep)
			t.splitTabs(x.ElseList, sep)
		}
		nodes = append(nodes, n)
	}
	list.Nodes = nodes
}

func (*table) splitTextNode(n *parse.TextNode, sep parse.Node) []parse.Node {
	parts := bytes.Split(n.Text, []byte{'\t'})
	if len(parts) == 1 {
		return []parse.Node{n}
	}
	out := make([]parse.Node, 0, len(parts)*2)
	for i, part := range parts {
		if i > 0 {
			out = append(out, sep)
		}
		if len(part) > 0 {
			out = append(out, &parse.TextNode{NodeType: parse.NodeText, Pos: n.Pos, Text: part})
		}
	}
	return out
}

// renderCells executes the row template and returns one string per template-defined cell.
func (t *table) renderCells(v any) ([]string, error) {
	t.scratch = t.scratch[:0]
	t.cellEnds = t.cellEnds[:0]
	err := t.tmpl.Execute(&t.scratch, v)
	if err != nil {
		return nil, err
	}
	out := t.scratch
	cells := make([]string, 0, len(t.cellEnds)+1)
	start := 0
	for _, end := range t.cellEnds {
		cells = append(cells, string(out[start:end]))
		start = end
	}
	return append(cells, string(out[start:])), nil
}

// addRecord keeps each cell line and its color on the same physical row.
func (t *table) addRecord(cells, colors []string) {
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
		count = max(count, len(lines[i]))
	}
	for k := range count {
		row := make([]text, len(lines))
		var rowColors []string
		if colors != nil {
			rowColors = make([]string, len(lines))
		}
		for i := range lines {
			if k >= len(lines[i]) {
				continue
			}
			row[i] = text(lines[i][k])
			if rowColors != nil && i < len(colors) {
				rowColors[i] = colors[i]
			}
		}
		t.addRow(row, rowColors)
	}
}

// addRow queues one physical row, keeping exactly one cell per column.
func (t *table) addRow(texts []text, colors []string) {
	row := make([]cell, len(t.columns))
	for i := range row {
		if i >= len(texts) || len(texts[i]) == 0 {
			continue
		}
		row[i] = cell{
			text:  texts[i],
			width: texts[i].truncateVisible(math.MaxInt32, ' ').width(),
		}
		if i < len(colors) {
			row[i].color = colors[i]
		}
	}
	t.pending = append(t.pending, row)
}

func (t *table) setColumnScale(name string, palette []string) error {
	if !t.autoTemplate {
		return nil
	}
	meta, err := t.columnMetadata(name)
	if err != nil {
		return err
	}
	toScalar, err := t.scaleScalar(meta.typ, meta.kind)
	if err != nil {
		return fmt.Errorf("column %q: %w", name, err)
	}
	t.ensureAutoTemplateMaps()
	t.columnScales[name] = &columnScale{
		toScalar:    toScalar,
		palette:     append([]string(nil), palette...),
		roundFloats: t.isFloatKind(meta.kind),
	}
	return nil
}

func (t *table) scaleScalar(typ reflect.Type, kind reflect.Kind) (greenRedScalarFn, error) {
	if typ == timeType {
		return t.scaleTimeScalar, nil
	}
	if typ == timeDurationType {
		return t.scaleDurationScalar, nil
	}
	return t.scaleNumericScalar(kind, typ)
}

func (t *table) scaleNumericScalar(kind reflect.Kind, typ reflect.Type) (greenRedScalarFn, error) {
	if t.isIntKind(kind) {
		return t.scaleIntScalar, nil
	}
	if t.isUintKind(kind) {
		return t.scaleUintScalar, nil
	}
	if t.isFloatKind(kind) {
		return t.scaleFloatScalar, nil
	}
	return nil, fmt.Errorf("unsupported type %s for green-red scale", typ)
}

func (t *table) scaleTimeScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok {
		return 0, false
	}
	ts, ok := raw.Interface().(time.Time)
	if !ok {
		return 0, false
	}
	return float64(ts.UnixNano()), true
}

func (t *table) scaleDurationScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok {
		return 0, false
	}
	d, ok := raw.Interface().(time.Duration)
	if !ok {
		return 0, false
	}
	return float64(d), true
}

func (t *table) scaleIntScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok || !t.isIntKind(raw.Kind()) {
		return 0, false
	}
	return float64(raw.Int()), true
}

func (t *table) scaleUintScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok || !t.isUintKind(raw.Kind()) {
		return 0, false
	}
	return float64(raw.Uint()), true
}

func (t *table) scaleFloatScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok || !t.isFloatKind(raw.Kind()) {
		return 0, false
	}
	return raw.Float(), true
}

func (t *table) scalarValue(v any) (reflect.Value, bool) {
	if v == nil {
		return reflect.Value{}, false
	}
	raw := reflect.ValueOf(v)
	for raw.Kind() == reflect.Pointer {
		if raw.IsNil() {
			return reflect.Value{}, false
		}
		raw = raw.Elem()
	}
	return raw, true
}

func (*table) isFloatKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func (*table) isIntKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	default:
		return false
	}
}

func (*table) isUintKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}

func (t *table) emit(ev tableEvent) {
	if t.eventSink == nil {
		return
	}
	t.eventSink(ev)
}

func (t *table) headers() error {
	if t.autoTemplate {
		return t.autoHeaders()
	}
	headers, err := t.extractFromNode(t.tmpl.Root)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	lookup := make(map[string]*fieldMetadata, len(t.metadata))
	for _, f := range t.metadata {
		lookup[f.name] = f
	}
	t.columns = make([]tableColumn, len(headers))
	columns := make([]tableColumnInfo, len(headers))
	bold := make([]text, len(headers))
	for i := range headers {
		meta, ok := lookup[headers[i]]
		if !ok {
			meta = &fieldMetadata{
				header:     strings.ToUpper(headers[i]),
				kind:       reflect.String,
				autoHeader: true,
			}
		}
		t.columns[i].meta = meta
		columns[i] = tableColumnInfo{
			Header: meta.header,
			Kind:   meta.kind.String(),
		}
		bold[i] = mkBold(meta.header)
	}
	if t.suppressHeaders {
		return nil
	}
	if t.eventSink != nil {
		t.emit(tableBegin{Columns: columns})
	} else {
		t.addRow(bold, nil)
	}

	return nil
}

// autoHeaders emits headers directly from reflected metadata in auto-template mode.
func (t *table) autoHeaders() error {
	t.columns = make([]tableColumn, len(t.metadata))
	columns := make([]tableColumnInfo, len(t.metadata))
	headers := make([]text, len(t.metadata))
	for i, meta := range t.metadata {
		t.columns[i].meta = meta
		columns[i] = tableColumnInfo{
			Header: meta.header,
			Kind:   meta.kind.String(),
		}
		headers[i] = mkBold(meta.header)
	}
	if t.suppressHeaders {
		return nil
	}
	if t.eventSink != nil {
		t.emit(tableBegin{Columns: columns})
		return nil
	}
	t.addRow(headers, nil)
	return nil
}

// autoRowTemplate applies include/skip filters and format rules to reflected fields.
func (t *table) autoRowTemplate() string {
	fields := t.filteredFields()
	t.metadata = fields
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = t.columnRenderTemplate(field)
	}
	return strings.Join(parts, "\t")
}

// filteredFields applies include/skip selectors while preserving struct field order.
func (t *table) filteredFields() structFields {
	if len(t.includeColumns) == 0 && len(t.skipColumns) == 0 {
		return t.metadata
	}
	out := make(structFields, 0, len(t.metadata))
	for _, field := range t.metadata {
		if len(t.includeColumns) > 0 {
			included, ok := t.includeColumns[field.name]
			if !ok || !included {
				continue
			}
		}
		skip, ok := t.skipColumns[field.name]
		if ok && skip {
			continue
		}
		out = append(out, field)
	}
	return out
}

// columnRenderTemplate resolves per-column template, name formatter, type formatter, then default.
func (t *table) columnRenderTemplate(meta *fieldMetadata) string {
	tmpl, ok := t.columnTemplates[meta.name]
	if ok {
		return tmpl
	}
	fnName, ok := t.columnNameFormats[meta.name]
	if ok {
		return formatterTemplate(fnName, meta)
	}
	fnName, ok = t.columnTypeFormats[meta.typ]
	if ok {
		return formatterTemplate(fnName, meta)
	}
	return meta.Template()
}

// formatterTemplate calls the formatter, rendering nil pointer fields as empty
// because text/template cannot dereference nil before the formatter runs.
func formatterTemplate(fnName string, meta *fieldMetadata) string {
	call := "{{" + fnName + " ." + meta.name + "}}"
	if meta.pointer {
		return "{{if ." + meta.name + "}}" + call + "{{end}}"
	}
	return call
}

// templateFuncs merges shared color functions with per-table formatter functions.
func (t *table) templateFuncs() template.FuncMap {
	funcs := make(template.FuncMap, len(colorFns)+len(t.customTemplateFuncs)+2)
	for name, fn := range colorFns {
		funcs[name] = fn
	}
	funcs["tableString"] = tableString
	funcs["__cellSep"] = t.cellSeparator
	for name, fn := range t.customTemplateFuncs {
		funcs[name] = fn
	}
	return funcs
}

// registerTemplateFunc stores a formatter under a unique name.
func (t *table) registerTemplateFunc(prefix string, fn any) string {
	t.ensureAutoTemplateMaps()
	name := fmt.Sprintf("%s%d", prefix, t.templateFuncSeq)
	t.templateFuncSeq++
	t.customTemplateFuncs[name] = fn
	return name
}

// ensureAutoTemplateMaps initializes lazy maps used by auto-template options.
func (t *table) ensureAutoTemplateMaps() {
	if t.customTemplateFuncs == nil {
		t.customTemplateFuncs = template.FuncMap{}
	}
	if t.columnTypeFormats == nil {
		t.columnTypeFormats = map[reflect.Type]string{}
	}
	if t.columnNameFormats == nil {
		t.columnNameFormats = map[string]string{}
	}
	if t.columnTemplates == nil {
		t.columnTemplates = map[string]string{}
	}
	if t.includeColumns == nil {
		t.includeColumns = map[string]bool{}
	}
	if t.skipColumns == nil {
		t.skipColumns = map[string]bool{}
	}
	if t.columnScales == nil {
		t.columnScales = map[string]*columnScale{}
	}
}

// columnMetadata resolves and validates a struct field path used by column options.
func (t *table) columnMetadata(name string) (*fieldMetadata, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("column name cannot be empty")
	}
	for _, meta := range t.metadata {
		if meta.name == name {
			return meta, nil
		}
	}
	return nil, fmt.Errorf("unknown column %q", name)
}

// columnSet validates column names and returns a set.
func (t *table) columnSet(names []string) (map[string]bool, error) {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		_, err := t.columnMetadata(name)
		if err != nil {
			return nil, err
		}
		set[name] = true
	}
	return set, nil
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

// scaleColors observes the record's scaled values once and returns a color per
// column, or nil when no scale is configured.
func (t *table) scaleColors(v any) []string {
	if len(t.columnScales) == 0 {
		return nil
	}
	colors := make([]string, len(t.columns))
	for i := range t.columns {
		meta := t.columns[i].meta
		scale, ok := t.columnScales[meta.name]
		if !ok || scale == nil {
			continue
		}
		raw, ok := t.fieldValueByPath(v, meta.name)
		if !ok {
			continue
		}
		colors[i] = scale.color(raw)
	}
	return colors
}

func (t *table) fieldValueByPath(v any, path string) (any, bool) {
	raw, ok := t.scalarValue(v)
	if !ok {
		return nil, false
	}
	for _, name := range strings.Split(path, ".") {
		if raw.Kind() != reflect.Struct {
			return nil, false
		}
		field := raw.FieldByName(name)
		if !field.IsValid() {
			return nil, false
		}
		for field.Kind() == reflect.Pointer {
			if field.IsNil() {
				return nil, false
			}
			field = field.Elem()
		}
		raw = field
	}
	if !raw.IsValid() || !raw.CanInterface() {
		return nil, false
	}
	return raw.Interface(), true
}

// padded writes cell aligned within column col.
func (t *table) padded(buf *bytes.Buffer, cell text, col int) error {
	padding := t.columns[col].width - cell.width()
	if t.columns[col].meta.alignRight { // right align
		err := t.pad(buf, padding)
		if err != nil {
			return err
		}
	}
	_, err := buf.Write(cell)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if !t.columns[col].meta.alignRight { // left align
		err = t.pad(buf, padding)
		if err != nil {
			return err
		}
	}
	return nil
}

func (*table) pad(buf *bytes.Buffer, padding int) error {
	for range padding {
		err := buf.WriteByte(' ')
		if err != nil {
			return fmt.Errorf("pad: %w", err)
		}
	}
	return nil
}

// fitPending truncates queued rows to their column widths and moves them to rows.
func (t *table) fitPending() {
	if !t.locked {
		t.locked = true
		t.allocateWidths()
	}
	for _, line := range t.pending {
		row := make([]text, len(line))
		for i, c := range line {
			if len(c.text) == 0 {
				// value is not available, but we need to insert something
				// to keep the table structure intact.
				row[i] = text(" ")
				continue
			}
			row[i] = c.text.truncateVisible(t.columns[i].maxVisible, ' ')
			if c.color != "" {
				row[i] = append(append(text(c.color), row[i]...), reset...)
			}
		}
		t.rows = append(t.rows, row)
	}
	t.pending = t.pending[:0]
}

// allocateWidths shrinks the widest first-batch columns evenly until the padded
// row fits maxWidth, never below colMinWidth visible characters per column.
func (t *table) allocateWidths() {
	n := len(t.columns)
	if n == 0 || len(t.pending) == 0 {
		return
	}
	need := make([]int, n)
	for _, line := range t.pending {
		for i, c := range line {
			need[i] = max(need[i], c.width)
		}
	}
	floor := t.colMinWidth + 1 // minimum visible text plus truncation tailer
	budget := t.maxWidth - n*t.cellPad
	limits := make([]int, n)
	for i := range limits {
		// reserve the minimum even for columns empty in the first batch
		limits[i] = floor
		budget -= limits[i]
	}
	for budget > 0 {
		grew := false
		for i := range limits {
			if budget > 0 && limits[i] < need[i] {
				limits[i]++
				budget--
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	// shrink widths to what the fitted cells actually use
	used := make([]int, n)
	for _, line := range t.pending {
		for i, c := range line {
			if len(c.text) == 0 {
				continue
			}
			used[i] = max(used[i], c.text.truncateVisible(limits[i], ' ').width())
		}
	}
	for i := range t.columns {
		// widths lock after the first batch, so later values need the minimum
		used[i] = max(used[i], floor)
		t.columns[i].maxVisible = used[i]
		t.columns[i].width = used[i] + t.cellPad
	}
}

//nolint:cyclop // TODO: maybe refactor later
func (t *table) extractFromNode(x parse.Node) ([]string, error) {
	switch n := x.(type) {
	case *parse.ListNode:
		return t.extractFromList(n)
	case *parse.FieldNode:
		return []string{strings.Join(n.Ident, ".")}, nil
	case *parse.ActionNode:
		return t.extractFromNode(n.Pipe)
	case *parse.TextNode:
		return []string{}, nil
	case *parse.IfNode:
		return t.extractFromNode(n.Pipe)
	case *parse.RangeNode:
		return nil, errors.New("range is not supported")
	case *parse.WithNode:
		return nil, errors.New("with is not supported")
	case *parse.TemplateNode:
		return nil, errors.New("template is not supported in header")
	case *parse.VariableNode:
		return []string{n.String()}, nil
	case *parse.CommandNode:
		return t.extractFromCommand(n)
	case *parse.PipeNode:
		return t.extractFromPipe(n)
	case *parse.ChainNode:
		return nil, errors.New("chain is not supported")
	case *parse.DotNode:
		return []string{n.String()}, nil
	case *parse.IdentifierNode:
		return []string{}, nil
	case *parse.NumberNode:
		return []string{}, nil
	case *parse.StringNode:
		return []string{}, nil
	default:
		return nil, fmt.Errorf("unknown node type: %T", x)
	}
}

func (t *table) extractFromPipe(n *parse.PipeNode) ([]string, error) {
	var headers []string
	for _, cmd := range n.Cmds {
		out, err := t.extractFromNode(cmd)
		if err != nil {
			return nil, err
		}
		headers = append(headers, out...)
	}
	return headers, nil
}

func (t *table) extractFromCommand(n *parse.CommandNode) ([]string, error) {
	var headers []string
	for _, arg := range n.Args {
		out, err := t.extractFromNode(arg)
		if err != nil {
			return nil, err
		}
		headers = append(headers, out...)
	}
	if len(headers) > 1 {
		return nil, errors.New("command has multiple arguments: " + strings.Join(headers, ", "))
	}
	// we only support single argument commands like: {{.Field | printf "%d"}}
	// so we just return the argument headers
	return headers, nil
}

func (t *table) extractFromList(n *parse.ListNode) ([]string, error) {
	var headers []string
	for _, node := range n.Nodes {
		out, err := t.extractFromNode(node)
		if err != nil {
			return nil, err
		}
		headers = append(headers, out...)
	}

	return headers, nil
}

func mkBold(s string) text {
	return text("\x1b[1m" + s + "\x1b[0m")
}

// theoretically we could make this public with additional options.
type fieldMetadata struct {
	header     string
	name       string
	autoHeader bool
	alignRight bool
	stringer   bool
	kind       reflect.Kind
	typ        reflect.Type
	pointer    bool
}

func (f *fieldMetadata) Template() string {
	if f.stringer {
		return "{{tableString ." + f.name + "}}"
	}
	switch f.kind {
	case reflect.Bool:
		return "{{if ." + f.name + "}}yes{{else}}no{{end}}"
	case reflect.Float32, reflect.Float64:
		return "{{printf \"%.2f\" ." + f.name + "}}"
	default:
		return "{{." + f.name + "}}"
	}
}

type structFields []*fieldMetadata

func (s structFields) Template() string {
	parts := make([]string, len(s))
	for i, col := range s {
		parts[i] = col.Template()
	}

	return strings.Join(parts, "\t")
}

// structFieldsForType reflects columns from static type information only, so
// interface element types are rejected: there is no value to infer a schema from.
func structFieldsForType(rt reflect.Type) (structFields, error) {
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() == reflect.Interface {
		return nil, fmt.Errorf("interface type %s is not supported, use a concrete struct type", rt)
	}
	if rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct or pointer to struct, got %s", rt.Kind())
	}

	return reflectStructFields(rt, map[reflect.Type]struct{}{})
}

//nolint:cyclop // TODO: fix
func reflectStructFields(rt reflect.Type, stack map[reflect.Type]struct{}) (structFields, error) {
	_, ok := stack[rt]
	if ok {
		return nil, nil
	}
	stack[rt] = struct{}{}
	defer delete(stack, rt)
	var out structFields
	for f := range rt.Fields() {
		f := f
		if !f.IsExported() {
			continue
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = f.Type.Elem()
		}
		isStringer := typeImplementsStringer(ft)
		if ft.Kind() == reflect.Struct && !isStringer {
			nested, err := reflectStructFields(ft, stack)
			if err != nil {
				return nil, fmt.Errorf("nested %s: %w", f.Name, err)
			}
			for _, n := range nested {
				if n.autoHeader {
					n.header = strings.ToUpper(f.Name + "." + n.header)
				}
				n.name = f.Name + "." + n.name
				out = append(out, n)
			}
			continue
		}
		meta, err := reflectFieldMetadata(ft, f.Tag, f.Name)
		if errors.Is(err, errCannotUse) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		meta.pointer = f.Type.Kind() == reflect.Pointer
		out = append(out, meta)
	}

	return out, nil
}

var errCannotUse = errors.New("cannot use")

func reflectFieldMetadata(ft reflect.Type, tag reflect.StructTag, name string) (*fieldMetadata, error) {
	meta := fieldMetadata{
		autoHeader: tag.Get("header") == "",
		kind:       ft.Kind(),
		name:       name,
		stringer:   typeImplementsStringer(ft),
		typ:        ft,
	}
	switch ft.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		meta.alignRight = true
	case reflect.Bool, reflect.String:
		meta.kind = ft.Kind()
	default:
		if meta.autoHeader && !meta.stringer {
			return nil, fmt.Errorf("%w %s without explicit header tag", errCannotUse, ft.Kind())
		}
	}
	if tag.Get("header") == "" {
		meta.header = strings.ToUpper(name)
	} else {
		queue := strings.Split(tag.Get("header"), ",")
		meta.header = queue[0]
		queue = queue[1:]
		for len(queue) > 0 {
			option := strings.TrimSpace(queue[0])
			queue = queue[1:]
			switch option {
			case "align-right":
				meta.alignRight = true
			case "align-left":
				meta.alignRight = false
			default:
				return nil, fmt.Errorf("unknown header option: %q", option)
			}
		}
	}

	return &meta, nil
}

// tableString stringifies values and supports pointer-receiver String methods.
func tableString(v any) string {
	if v == nil {
		return fmt.Sprint(v)
	}
	value := reflect.ValueOf(v)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		// typed-nil receivers may dereference in String; fmt recovers to "<nil>"
		return fmt.Sprint(v)
	}
	x, ok := v.(fmt.Stringer)
	if ok {
		return x.String()
	}
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return fmt.Sprint(v)
		}
		value = value.Elem()
	}
	ptr := reflect.New(value.Type())
	ptr.Elem().Set(value)
	x, ok = ptr.Interface().(fmt.Stringer)
	if ok {
		return x.String()
	}
	return fmt.Sprint(v)
}

func typeImplementsStringer(typ reflect.Type) bool {
	if typ == nil {
		return false
	}
	if typ.Implements(fmtStringerType) {
		return true
	}
	return typ.Kind() != reflect.Pointer && reflect.PointerTo(typ).Implements(fmtStringerType)
}
