// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"strings"
	"text/template"
)

// projection is the content of a choice list set by [WithTemplate]. It
// replaces the Value and Details slots of the
// theme and leaves the presentation alone. view adapts a raw item to the
// payload of those slots; nil passes it unchanged.
type projection struct {
	mainSource, detailsSource     string
	mainTemplate, detailsTemplate *template.Template
	view                          func(any) any
}

// slotItem is the payload of the Value and Details slots for item.
func (p *projection) slotItem(item any) any {
	if p.view == nil {
		return item
	}
	return p.view(item)
}

// setProjection preserves WithTemplate expressions while leaving presentation to the theme.
func (p *projection) setProjection(main string, details ...string) {
	p.mainSource = fmt.Sprintf(`{{ __content %s }}`, main)
	p.detailsSource = ""
	if len(details) > 0 {
		p.detailsSource = fmt.Sprintf(`{{ __content (%s) }}`, strings.Join(details, `) ", " (`))
	}
}

// parse checks the projection using helpers replaced at destination binding.
func (p *projection) parse(c *compiledTheme, name string) error {
	funcs := c.funcs(color.ProfilePlain, roleStyles{})
	funcs["__content"] = span{}.helper()
	tmpl := template.New(name).Funcs(funcs)
	for _, part := range []struct {
		name, source string
		dest         **template.Template
	}{
		{"content", p.mainSource, &p.mainTemplate},
		{"details", p.detailsSource, &p.detailsTemplate},
	} {
		if part.source == "" {
			continue
		}
		parsed, err := tmpl.New("WithTemplate " + part.name).Parse(part.source)
		if err != nil {
			return fmt.Errorf("%s: %w", part.name, err)
		}
		*part.dest = parsed
	}
	return nil
}

// rebind clones the parsed templates before installing destination-bound helpers.
func (p *projection) rebind(funcs template.FuncMap) error {
	for _, t := range []**template.Template{&p.mainTemplate, &p.detailsTemplate} {
		if *t == nil {
			continue
		}
		clone, err := (*t).Clone()
		if err != nil {
			return fmt.Errorf("%s: %w", (*t).Name(), err)
		}
		*t = clone.Funcs(funcs)
	}
	return nil
}

// choiceSlots are the theme slots of a choice family bound to one destination.
type choiceSlots struct {
	value, details, active, inactive, answer boundSlot
}

// bind binds the item and answer slots of the family to funcs.
func (f choiceFamily) bind(funcs template.FuncMap) (s choiceSlots, _ error) {
	var errs [5]error
	s.value, errs[0] = f.Value.bind(funcs)
	s.details, errs[1] = f.Details.bind(funcs)
	s.active, errs[2] = f.Active.bind(funcs)
	s.inactive, errs[3] = f.Inactive.bind(funcs)
	s.answer, errs[4] = f.Answer.bind(funcs)
	return s, errors.Join(errs[:]...)
}

// chrome renders the label and binds the hint, which every frame renders
// with the count of the items below the window.
func (f choiceFamily) chrome(funcs template.FuncMap, label string) (head ansi.Text, hint boundSlot, _ error) {
	slot, err := f.Label.bind(funcs)
	if err != nil {
		return nil, boundSlot{}, err
	}
	head, err = slot.render(labelView{label})
	if err != nil {
		return nil, boundSlot{}, err
	}
	hint, err = f.Hint.bind(funcs)
	return head, hint, err
}

// projectContent renders the projection or falls back to the bound Value slot.
func (p *projection) projectContent(value boundSlot, item any) (ansi.Text, error) {
	if p.mainTemplate == nil {
		return value.render(p.slotItem(item))
	}
	var out ansi.Text
	err := p.mainTemplate.Execute(&out, item)
	if err != nil {
		return nil, fmt.Errorf("content: %w", err)
	}
	return out.BeforeNewline(), nil
}

// projectDetails uses projected details when a projection is set, otherwise the bound Details slot.
func (p *projection) projectDetails(details boundSlot, item any) (ansi.Text, error) {
	switch {
	case p.mainTemplate == nil:
		return details.render(p.slotItem(item))
	case p.detailsTemplate == nil:
		return nil, nil
	}
	var out ansi.Text
	err := p.detailsTemplate.Execute(&out, item)
	if err != nil {
		return nil, fmt.Errorf("details: %w", err)
	}
	return out.BeforeNewline(), nil
}

// choiceView is the payload of the Active and Inactive slots: the projected
// content of one item, its source Index and its state. A dropdown has no
// check state, so Selected is false.
type choiceView struct {
	Value, Details   string
	Index            int
	Active, Selected bool
}
