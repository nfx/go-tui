// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"text/template"
)

// roleStyles are the encoded foregrounds of the semantic helpers and of the
// guide for one destination. An empty style keeps the terminal default.
type roleStyles struct {
	foreground, muted, guide, accent, success, failure text
}

// destination is what the built-in template helpers depend on: what one output
// accepts and shows.
type destination struct {
	profile colorProfile
	roles   roleStyles
	symbols Symbols
}

// newSpan defines a style wrapper that leaves text unchanged when no sequences are set.
func (d destination) newSpan(closer string, seqs ...text) span {
	s := span{closer: closer}
	for _, seq := range seqs {
		if len(seq) == 0 {
			continue
		}
		set, _ := seq.scan().sgrEffect()
		s.open = append(s.open, sgrCode{seq: seq.String(), channels: set})
		s.channels |= set
	}
	return s
}

// roleSpan wraps text in a role style: a color, or the faint fallback of
// the guide, which needs its own reset.
func (d destination) roleSpan(role text) span {
	s := d.newSpan(fgDefault, role)
	if s.channels&sgrIntensity != 0 {
		s.closer = intensityDefault
	}
	return s
}

// named returns the attribute helpers and ago.
func (d destination) named() template.FuncMap {
	var namedStyles = map[string]string{
		"bold":      bold,
		"dim":       dim,
		"italic":    italic,
		"underline": underline,
		"strike":    strikethrough,
	}
	funcs := make(template.FuncMap, len(namedStyles)+1)
	for name, seq := range namedStyles {
		funcs[name] = d.newSpan(reset, text(d.profile.named(text(seq)))).helper()
	}
	funcs["ago"] = ago
	return funcs
}

// funcs returns all built-in helpers bound to the destination.
func (d destination) funcs() template.FuncMap {
	funcs := d.named()
	for name, seq := range map[string]text{
		"text":    d.roles.foreground,
		"muted":   d.roles.muted,
		"guide":   d.roles.guide,
		"accent":  d.roles.accent,
		"success": d.roles.success,
		"error":   d.roles.failure,
	} {
		funcs[name] = d.roleSpan(seq).helper()
	}
	funcs["fg"] = d.color(false)
	funcs["bg"] = d.color(true)
	funcs["symbol"] = d.symbols.named
	funcs["label"] = choiceLabels{}.rowLabel
	return funcs
}

// color returns a foreground or background helper that validates colors at execution.
func (d destination) color(background bool) func(string, ...any) (string, error) {
	name, closer := "fg", fgDefault
	if background {
		name, closer = "bg", bgDefault
	}
	return func(c string, args ...any) (string, error) {
		rgb, err := RGB(c).parse()
		if err == nil && c == "" {
			err = errors.New(`"": want "#RRGGBB" or "#RGB"`)
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		seq := d.profile.encode(rgb, background)
		s := d.newSpan(closer, seq)
		return s.wrap(s.formatArgs(args)).String(), nil
	}
}
