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
	"reflect"
	"strings"
	"text/template"
	"text/template/parse"
)

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

	return t.flush()
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

	return t.flush()
}

func TableX[T any](w io.Writer, iterator []T, o ...opt) error {
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

	return t.flush()
}

// table is an alternative to text/tabwriter that supports ANSI colors and
// truncation of wide cells. It uses text/template to render each row.
// The first row is used to extract the headers from the template.
type table struct {
	w           io.Writer
	buf         []byte
	tmpl        *template.Template
	columns     []tableColumn
	metadata    structFields
	rows        [][]string
	curr        []string
	cellPad     int
	batchSize   int
	maxWidth    int
	colMinWidth int
	locked      bool
	consumed    int
}

type tableColumn struct {
	width int
	meta  *fieldMetadata
}

func newTable[T any](w io.Writer, rowTmpl string, o ...opt) (*table, error) {
	metadata, err := structFieldsFor[T]()
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	if rowTmpl == "" {
		rowTmpl = metadata.Template()
		if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
			slog.Debug("table: generated template", "template", rowTmpl)
		}
	}
	tmpl, err := template.New("row").Funcs(colorFns).Parse(rowTmpl)
	if err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	t := &table{
		w:           w,
		tmpl:        tmpl,
		metadata:    metadata,
		buf:         []byte{},
		maxWidth:    80,
		batchSize:   10,
		cellPad:     1,
		colMinWidth: 1,
	}
	err = opts(o).Apply(t)
	if err != nil {
		return nil, err
	}
	err = t.headers()
	if err != nil {
		return nil, fmt.Errorf("headers: %w", err)
	}

	return t, nil
}

func (t *table) Append(v any) error {
	err := t.tmpl.Execute(t, v)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	_, err = t.Write([]byte("\n"))
	if err != nil {
		return fmt.Errorf("newline: %w", err)
	}
	t.consumed++
	if t.consumed%t.batchSize == 0 {
		err = t.flush()
		if err != nil {
			return fmt.Errorf("flush: %w", err)
		}
	}

	return nil
}

func (t *table) Write(p []byte) (n int, err error) {
	t.buf = append(t.buf, p...)

	return len(p), nil
}

func (t *table) headers() error {
	headers, err := t.extractFromNode(t.tmpl.Root)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	lookup := make(map[string]*fieldMetadata, len(t.metadata))
	for _, f := range t.metadata {
		lookup[f.name] = f
	}
	t.columns = make([]tableColumn, len(headers))
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
		headers[i] = mkBold(meta.header)
	}
	t.buf = append(t.buf, []byte(strings.Join(headers, "\t")+"\n")...)

	return nil
}

func (t *table) flush() error {
	t.currentBuffer()
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

func (t *table) padded(buf *bytes.Buffer, cell string, col int) error {
	padding := t.columns[col].width - width([]byte(cell))
	if t.columns[col].meta.alignRight { // right align
		err := t.pad(buf, padding)
		if err != nil {
			return err
		}
	}
	_, err := buf.WriteString(cell)
	if err != nil {
		return fmt.Errorf("write string: %w", err)
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

func (t *table) currentBuffer() {
	col := 0
	var cell []byte
	maxLen := t.maxWidth - ((len(t.columns) - 1) * (t.colMinWidth + t.cellPad))
	for _, b := range t.buf {
		switch b {
		case '\t':
			cell = t.currentCell(cell, col, maxLen)
			col++
		case '\n':
			cell = t.currentCell(cell, col, maxLen)
			row := make([]string, len(t.columns))
			copy(row, t.curr)
			t.rows = append(t.rows, row)
			t.curr = t.curr[:0]
			col = 0
		default:
			cell = append(cell, b)
		}
	}
	t.buf = t.buf[:0]
	if !t.locked {
		t.locked = true
	}
}

func (t *table) currentCell(cell []byte, col, maxLen int) []byte {
	if len(cell) == 0 {
		// value is not available, but we need to insert something
		// to keep the table structure intact.
		t.curr = append(t.curr, " ")

		return []byte{}
	}
	if t.locked {
		maxLen = t.columns[col].width
	}
	cell = truncateVisible(cell, maxLen, ' ')
	t.curr = append(t.curr, string(cell))
	if !t.locked {
		t.columns[col].width = max(t.columns[col].width, width(cell)+t.cellPad)
	}
	cell = cell[:0]

	return cell
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

func mkBold(s string) string {
	return "\x1b[1m" + s + "\x1b[0m"
}

// theoretically we could make this public with additional options.
type fieldMetadata struct {
	header     string
	name       string
	autoHeader bool
	alignRight bool
	kind       reflect.Kind
}

func (f *fieldMetadata) Template() string {
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

func structFieldsFor[T any]() (structFields, error) {
	var t T
	rt := reflect.ValueOf(t).Type()
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct or pointer to struct, got %s", rt.Kind())
	}

	return reflectStructFields(rt)
}

//nolint:cyclop // TODO: fix
func reflectStructFields(rt reflect.Type) (structFields, error) {
	var out structFields
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = f.Type.Elem()
		}
		_, isStringer := ft.MethodByName("String")
		if ft.Kind() == reflect.Struct && !isStringer {
			nested, err := reflectStructFields(ft)
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
	}
	switch ft.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		meta.alignRight = true
	case reflect.Bool, reflect.String:
		meta.kind = ft.Kind()
	default:
		if meta.autoHeader {
			return nil, fmt.Errorf("%w %s without explicit header tag", errCannotUse, ft.Kind())
		}
	}
	queue := strings.Split(tag.Get("header"), ",")
	if len(queue) == 0 {
		meta.header = strings.ToUpper(name)
	} else {
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
