// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"strings"
	"text/template"
	"text/template/parse"
)

func Table[T any](w io.Writer, rowTmpl string, iterator iter.Seq2[T, error], o ...opt) error {
	t, err := newTable(w, rowTmpl, o...)
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

// table is an alternative to text/tabwriter that supports ANSI colors and
// truncation of wide cells. It uses text/template to render each row.
// The first row is used to extract the headers from the template.
type table struct {
	w           io.Writer
	buf         []byte
	tmpl        *template.Template
	columns     []int
	rows        [][]string
	curr        []string
	cellPad     int
	batchSize   int
	maxWidth    int
	colMinWidth int
	locked      bool
	consumed    int
}

func newTable(w io.Writer, rowTmpl string, o ...opt) (*table, error) {
	tmpl, err := template.New("row").Funcs(colorFns).Parse(rowTmpl)
	if err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	t := &table{
		w:           w,
		tmpl:        tmpl,
		buf:         []byte{},
		maxWidth:    80,
		batchSize:   10,
		cellPad:     1,
		colMinWidth: 1,
	}
	for _, fn := range o {
		err = fn(t)
		if err != nil {
			return nil, fmt.Errorf("option: %w", err)
		}
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
	t.columns = make([]int, len(headers))
	for i := range headers {
		headers[i] = mkBold(strings.ToUpper(headers[i]))
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
	padding := t.columns[col] - width([]byte(cell))
	_, err := buf.WriteString(cell)
	if err != nil {
		return fmt.Errorf("write string: %w", err)
	}
	for range padding { // left align
		err = buf.WriteByte(' ')
		if err != nil {
			return fmt.Errorf("right pad: %w", err)
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
		return []byte{}
	}
	if t.locked {
		maxLen = t.columns[col]
	}
	cell = truncateVisible(cell, maxLen, ' ')
	t.curr = append(t.curr, string(cell))
	if !t.locked {
		t.columns[col] = max(t.columns[col], width(cell)+t.cellPad)
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
		return nil, errors.New("if is not supported")
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
