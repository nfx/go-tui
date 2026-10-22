// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/theme"
	"text/template"
	"text/template/parse"
)

// Slot is one parsed presentation slot. A hidden slot renders nothing
// and reserves no space, and so does a nil slot, which has no built-in yet.
type Slot struct {
	tmpl   *template.Template
	hidden bool
}

// compileSlot parses an isolated source, inheriting unset sources and allowing Hidden only for optional slots.
func compileSlot(in templated[theme.Template, *Slot], optional bool) (*Slot, error) {
	switch in.Src {
	case "":
		return in.Parent, nil
	case theme.Hidden:
		if !optional {
			return nil, fmt.Errorf("theme.Templates.%s: empty source is not allowed", in.Name)
		}
		return &Slot{hidden: true}, nil
	}
	tmpl, err := template.New(in.Name).Funcs(in.Funcs).Parse(templateSource(in.Src))
	if err == nil {
		err = colorLiterals(tmpl)
	}
	if err != nil {
		return nil, fmt.Errorf("theme.Templates.%s: %w", in.Name, err)
	}
	return &Slot{tmpl: tmpl}, nil
}

// colorLiterals checks the color argument of every fg and bg call that is a
// string literal, in every template of a slot, so that a typo fails the
// installation. Dynamic colors are checked when the template runs.
func colorLiterals(tmpl *template.Template) error {
	for _, tt := range tmpl.Templates() {
		if tt.Tree == nil {
			continue
		}
		if err := walkTemplates(tt.Tree, tt.Root, colorLiteral); err != nil {
			return err
		}
	}
	return nil
}

// walkTemplates calls visit for n and every node below it in tree.
func walkTemplates(tree *parse.Tree, n parse.Node, visit func(*parse.Tree, parse.Node) error) error {
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
		if err := walkTemplates(tree, c, visit); err != nil {
			return err
		}
	}
	return nil
}

// colorLiteral validates the color of an fg or bg command with a literal one.
func colorLiteral(tree *parse.Tree, node parse.Node) error {
	n, ok := node.(*parse.CommandNode)
	if !ok || len(n.Args) < 2 {
		return nil
	}
	id, isID := n.Args[0].(*parse.IdentifierNode)
	lit, isStr := n.Args[1].(*parse.StringNode)
	if !isID || !isStr || id.Ident != "fg" && id.Ident != "bg" {
		return nil
	}
	err := theme.RGB(lit.Text).Validate()
	if lit.Text == "" {
		err = errors.New(`"": want "#RRGGBB" or "#RGB"`)
	}
	if err != nil {
		loc, _ := tree.ErrorContext(n)
		return fmt.Errorf("%s: %s: %w", loc, id.Ident, err)
	}
	return nil
}

// Bind attaches destination helpers to a clone, preserving the installed slot.
func (s *Slot) Bind(Bind template.FuncMap) (BoundSlot, error) {
	if s == nil || s.hidden {
		return BoundSlot{}, nil
	}
	tmpl, err := s.tmpl.Clone()
	if err != nil {
		return BoundSlot{}, fmt.Errorf("theme.Templates.%s: %w", s.tmpl.Name(), err)
	}
	return BoundSlot{Tmpl: tmpl.Funcs(Bind)}, nil
}

// BoundSlot is a slot bound to one destination. The zero value is hidden.
type BoundSlot struct {
	Tmpl *template.Template
}

// Render returns complete slot output or an error, allowing only text and SGR.
func (b BoundSlot) Render(data any) (ansi.Text, error) {
	out, err := b.Execute(data)
	if err == nil {
		err = b.Styled(out)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Execute returns the slot output without checking it.
func (b BoundSlot) Execute(data any) (ansi.Text, error) {
	if b.Tmpl == nil {
		return nil, nil
	}
	var out ansi.Text
	err := b.Tmpl.Execute(&out, data)
	if err != nil {
		return nil, fmt.Errorf("theme.Templates.%s: %w", b.Tmpl.Name(), err)
	}
	return out, nil
}

// Styled reports slot output that is not only text and SGR.
func (b BoundSlot) Styled(out ansi.Text) error {
	if seg, ok := out.Unstyled(); ok {
		return fmt.Errorf("theme.Templates.%s: %q: want text and SGR only", b.Tmpl.Name(), seg.Text)
	}
	return nil
}

// templateSource returns the template text; it is empty for [Hidden].
func templateSource(t theme.Template) string {
	if t == theme.Hidden {
		return ""
	}
	return string(t)
}
