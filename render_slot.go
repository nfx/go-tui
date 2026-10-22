// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"text/template"
	"text/template/parse"
)

// compiledSlot is one parsed presentation slot. A hidden slot renders nothing
// and reserves no space, and so does a nil slot, which has no built-in yet.
type compiledSlot struct {
	tmpl   *template.Template
	hidden bool
}

// slot parses an isolated source, inheriting unset sources and allowing Hidden only for optional slots.
func (t *Templates) slot(in templated[Template, *compiledSlot], optional bool) (*compiledSlot, error) {
	switch in.Src {
	case "":
		return in.Parent, nil
	case Hidden:
		if !optional {
			return nil, fmt.Errorf("theme.Templates.%s: empty source is not allowed", in.Name)
		}
		return &compiledSlot{hidden: true}, nil
	}
	tmpl, err := template.New(in.Name).Funcs(in.Funcs).Parse(in.Src.source())
	if err == nil {
		err = t.colorLiterals(tmpl)
	}
	if err != nil {
		return nil, fmt.Errorf("theme.Templates.%s: %w", in.Name, err)
	}
	return &compiledSlot{tmpl: tmpl}, nil
}

// colorLiterals checks the color argument of every fg and bg call that is a
// string literal, in every template of a slot, so that a typo fails the
// installation. Dynamic colors are checked when the template runs.
func (t *Templates) colorLiterals(tmpl *template.Template) error {
	for _, tt := range tmpl.Templates() {
		if tt.Tree == nil {
			continue
		}
		if err := t.walk(tt.Tree, tt.Root, t.colorLiteral); err != nil {
			return err
		}
	}
	return nil
}

// walk calls visit for n and every node below it in tree.
func (t *Templates) walk(tree *parse.Tree, n parse.Node, visit func(*parse.Tree, parse.Node) error) error {
	if err := visit(tree, n); err != nil {
		return err
	}
	var next []parse.Node
	switch n := n.(type) {
	case *parse.ListNode:
		if n != nil {
			next = n.Nodes
		}
	case *parse.ActionNode:
		next = []parse.Node{n.Pipe}
	case *parse.IfNode:
		next = []parse.Node{n.Pipe, n.List, n.ElseList}
	case *parse.RangeNode:
		next = []parse.Node{n.Pipe, n.List, n.ElseList}
	case *parse.WithNode:
		next = []parse.Node{n.Pipe, n.List, n.ElseList}
	case *parse.TemplateNode:
		next = []parse.Node{n.Pipe}
	case *parse.PipeNode:
		if n != nil {
			for _, c := range n.Cmds {
				next = append(next, c)
			}
		}
	case *parse.ChainNode:
		next = []parse.Node{n.Node}
	case *parse.CommandNode:
		next = n.Args
	}
	for _, c := range next {
		if err := t.walk(tree, c, visit); err != nil {
			return err
		}
	}
	return nil
}

// colorLiteral validates the color of an fg or bg command with a literal one.
func (*Templates) colorLiteral(tree *parse.Tree, node parse.Node) error {
	n, ok := node.(*parse.CommandNode)
	if !ok || len(n.Args) < 2 {
		return nil
	}
	id, isID := n.Args[0].(*parse.IdentifierNode)
	lit, isStr := n.Args[1].(*parse.StringNode)
	if !isID || !isStr || id.Ident != "fg" && id.Ident != "bg" {
		return nil
	}
	err := RGB(lit.Text).Validate()
	if lit.Text == "" {
		err = errors.New(`"": want "#RRGGBB" or "#RGB"`)
	}
	if err != nil {
		loc, _ := tree.ErrorContext(n)
		return fmt.Errorf("%s: %s: %w", loc, id.Ident, err)
	}
	return nil
}

// bind attaches destination helpers to a clone, preserving the installed slot.
func (s *compiledSlot) bind(funcs template.FuncMap) (boundSlot, error) {
	if s == nil || s.hidden {
		return boundSlot{}, nil
	}
	tmpl, err := s.tmpl.Clone()
	if err != nil {
		return boundSlot{}, fmt.Errorf("theme.Templates.%s: %w", s.tmpl.Name(), err)
	}
	return boundSlot{tmpl: tmpl.Funcs(funcs)}, nil
}

// boundSlot is a slot bound to one destination. The zero value is hidden.
type boundSlot struct {
	tmpl *template.Template
}

// render returns complete slot output or an error, allowing only text and SGR.
func (b boundSlot) render(data any) (Text, error) {
	out, err := b.execute(data)
	if err == nil {
		err = b.styled(out)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// execute returns the slot output without checking it.
func (b boundSlot) execute(data any) (Text, error) {
	if b.tmpl == nil {
		return nil, nil
	}
	var out Text
	err := b.tmpl.Execute(&out, data)
	if err != nil {
		return nil, fmt.Errorf("theme.Templates.%s: %w", b.tmpl.Name(), err)
	}
	return out, nil
}

// styled reports slot output that is not only text and SGR.
func (b boundSlot) styled(out Text) error {
	if seg, ok := out.Unstyled(); ok {
		return fmt.Errorf("theme.Templates.%s: %q: want text and SGR only", b.tmpl.Name(), seg.Text)
	}
	return nil
}
