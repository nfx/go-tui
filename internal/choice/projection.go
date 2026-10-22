// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/render"
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
func (p *projection) parse(c *render.Compiled, name string) error {
	funcs := c.Funcs(color.ProfilePlain, render.Roles{})
	funcs["__content"] = render.Span{}.Helper()
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
	value, details, active, inactive, answer render.BoundSlot
}

// bindChoiceFamily binds the item and answer slots of the family to funcs.
func bindChoiceFamily(f render.ChoiceFamily, funcs template.FuncMap) (s choiceSlots, _ error) {
	var errs [5]error
	s.value, errs[0] = f.Value.Bind(funcs)
	s.details, errs[1] = f.Details.Bind(funcs)
	s.active, errs[2] = f.Active.Bind(funcs)
	s.inactive, errs[3] = f.Inactive.Bind(funcs)
	s.answer, errs[4] = f.Answer.Bind(funcs)
	return s, errors.Join(errs[:]...)
}

// chromeChoiceFamily renders the label and binds the hint, which every frame renders
// with the count of the items below the window.
func chromeChoiceFamily(
	f render.ChoiceFamily,
	funcs template.FuncMap,
	label string,
) (head ansi.Text, hint render.BoundSlot, _ error) {
	slot, err := f.Label.Bind(funcs)
	if err != nil {
		return nil, render.BoundSlot{}, err
	}
	head, err = slot.Render(render.LabelView{Label: label})
	if err != nil {
		return nil, render.BoundSlot{}, err
	}
	hint, err = f.Hint.Bind(funcs)
	return head, hint, err
}

// projectContent renders the projection or falls back to the bound Value slot.
func (p *projection) projectContent(value render.BoundSlot, item any) (ansi.Text, error) {
	if p.mainTemplate == nil {
		return value.Render(p.slotItem(item))
	}
	var out ansi.Text
	err := p.mainTemplate.Execute(&out, item)
	if err != nil {
		return nil, fmt.Errorf("content: %w", err)
	}
	return out.BeforeNewline(), nil
}

// projectDetails uses projected details when a projection is set, otherwise the bound Details slot.
func (p *projection) projectDetails(details render.BoundSlot, item any) (ansi.Text, error) {
	switch {
	case p.mainTemplate == nil:
		return details.Render(p.slotItem(item))
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
