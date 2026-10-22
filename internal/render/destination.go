// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/theme"
	"text/template"
)

// Roles are the encoded foregrounds of the semantic helpers and of the
// guide for one destination. An empty style keeps the terminal default.
type Roles struct {
	Foreground, Muted, Guide, Accent, Success, Failure ansi.Text
}

// destination is what the built-in template helpers depend on: what one output
// accepts and shows.
type destination struct {
	profile color.Profile
	roles   Roles
	symbols theme.Symbols
}

// newSpan defines a style wrapper that leaves text unchanged when no sequences are set.
func (d destination) newSpan(closer string, seqs ...ansi.Text) Span {
	s := Span{closer: closer}
	for _, seq := range seqs {
		if len(seq) == 0 {
			continue
		}
		set, _ := seq.Scan().SGREffect()
		s.open = append(s.open, sgrCode{seq: seq.String(), channels: set})
		s.channels |= set
	}
	return s
}

// roleSpan wraps text in a role style: a color, or the faint fallback of
// the guide, which needs its own reset.
func (d destination) roleSpan(role ansi.Text) Span {
	s := d.newSpan(color.FgDefault, role)
	if s.channels&ansi.SGRIntensity != 0 {
		s.closer = color.IntensityDefault
	}
	return s
}

// named returns the attribute helpers and ago.
func (d destination) named() template.FuncMap {
	var namedStyles = map[string]string{
		"bold":      ansi.Bold,
		"dim":       ansi.Dim,
		"italic":    ansi.Italic,
		"underline": ansi.Underline,
		"strike":    ansi.Strikethrough,
	}
	funcs := make(template.FuncMap, len(namedStyles)+1)
	for name, seq := range namedStyles {
		funcs[name] = d.newSpan(ansi.Reset, ansi.Text(d.profile.Named(ansi.Text(seq)))).Helper()
	}
	funcs["ago"] = ago
	return funcs
}

// funcs returns all built-in helpers bound to the destination.
func (d destination) funcs() template.FuncMap {
	funcs := d.named()
	for name, seq := range map[string]ansi.Text{
		"text":    d.roles.Foreground,
		"muted":   d.roles.Muted,
		"guide":   d.roles.Guide,
		"accent":  d.roles.Accent,
		"success": d.roles.Success,
		"error":   d.roles.Failure,
	} {
		funcs[name] = d.roleSpan(seq).Helper()
	}
	funcs["fg"] = d.color(false)
	funcs["bg"] = d.color(true)
	funcs["symbol"] = d.symbols.Named
	funcs["label"] = ChoiceLabels{}.rowLabel
	return funcs
}

// color returns a foreground or background helper that validates colors at execution.
func (d destination) color(background bool) func(string, ...any) (string, error) {
	name, closer := "fg", color.FgDefault
	if background {
		name, closer = "bg", color.BgDefault
	}
	return func(c string, args ...any) (string, error) {
		rgb, err := parseRGB(theme.RGB(c))
		if err == nil && c == "" {
			err = errors.New(`"": want "#RRGGBB" or "#RGB"`)
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		seq := d.profile.Encode(rgb, background)
		s := d.newSpan(closer, seq)
		return s.wrap(s.formatArgs(args)).String(), nil
	}
}
