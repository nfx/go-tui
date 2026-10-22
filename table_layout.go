// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"text/template"
	"text/template/parse"
)

// columnView is the data of the Table.Header slot.
type columnView struct {
	Header string
	Name   string // the field path, such as Owner.Name
	Kind   string // the reflect kind, such as string or int
}

// cellView is the data of the Table.Cell slot: one physical line of a
// formatted value, which may carry SGR and hyperlinks, in its column.
type cellView struct {
	columnView
	Value string
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

// carryStyles reopens on each line of a cell the SGR in effect at the end of
// the line before, so a style spanning a line break continues; truncation
// closes every line, so it cannot bleed into the next column.
func (*table) carryStyles(lines []string) {
	var open Text
	var active SGRChannel
	for k, line := range lines {
		if k > 0 && len(open) > 0 && line != "" {
			lines[k] = string(open) + line
		}
		for seg := range Text(line).Segments() {
			if _, sgr := seg.SGRParams(); !sgr {
				continue
			}
			set, cleared := seg.SGREffect()
			active = (active | set) &^ cleared
			if active == 0 {
				open = open[:0]
				continue
			}
			open = append(open, seg.Text...)
		}
	}
}

// decorate wraps each line of an automatic row template in its scale color
// and then in the Cell slot. Explicit row templates are left as rendered.
func (t *table) decorate(row []Text, colors []string) error {
	if !t.autoTemplate {
		return nil
	}
	for i, line := range row {
		if i >= len(t.columns) {
			break // addRow drops cells beyond the columns
		}
		line = t.cellText(line)
		if len(line) == 0 {
			continue
		}
		if i < len(colors) {
			line = t.look.style(Text(colors[i]), line)
		}
		meta := t.columns[i].meta
		out, err := t.cell.execute(cellView{columnView: t.column(meta), Value: line.String()})
		if err == nil {
			err = t.cellOutput(t.cell, out)
		}
		if err != nil {
			return fmt.Errorf("column %q: %w", meta.name, err)
		}
		row[i] = out
	}
	return nil
}

// padded writes cell aligned within column col.
func (t *table) padded(buf *bytes.Buffer, cell Text, col int) error {
	padding := t.columns[col].width - cell.Width()
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
		row := make([]Text, len(line))
		for i, c := range line {
			if len(c.text) == 0 {
				// value is not available, but we need to insert something
				// to keep the table structure intact.
				row[i] = Text(" ")
				continue
			}
			row[i] = c.text.TruncateVisible(t.columns[i].maxVisible, ' ')
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
			used[i] = max(used[i], c.text.TruncateVisible(limits[i], ' ').Width())
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
