// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prettyjson

import (
	"bytes"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/render"
)

type jsonState int

const (
	jsonRoot jsonState = iota
	jsonObject
	jsonArray
	jsonQuoted
	jsonKey
	jsonConst
	jsonValue
	jsonEscape
)

// jsonLevels is the number of key depth shades; deeper keys keep the last one.
const jsonLevels = 5

// jsonShades are the encoded shades of a token kind, by nesting depth.
type jsonShades []ansi.Text

// at returns the shade for depth, brightest at the top level.
func (s jsonShades) at(depth int) ansi.Text {
	if len(s) == 0 {
		return nil
	}
	return s[min(max(depth, 0), len(s)-1)]
}

// jsonLook is the encoded presentation of JSON tokens for one destination
// that accepts SGR. Keys are always bold, after their shade.
type jsonLook struct {
	keys  jsonShades // may be empty
	value ansi.Text  // prefix for strings, numbers and literals
	punct ansi.Text  // prefix for brackets and braces
}

// newJSONLook maps the roles of a binding onto JSON: keys are the accent,
// dimmed with depth, values the foreground and punctuation the muted role.
// A neutral role that keeps the terminal default, as on an unknown
// background where it could be nearly invisible, leaves values unstyled and
// makes punctuation bold. It is computed per operation: the theme, its
// background and the destination all decide it.
func newJSONLook(b render.Binding) jsonLook {
	look := jsonLook{value: b.Roles.Foreground, punct: b.Roles.Muted}
	if len(look.punct) == 0 {
		look.punct = ansi.Text(ansi.Bold)
	}
	if b.Profile < color.ProfileANSI16 {
		return look
	}
	pal := b.Palette
	top, ok := b.Theme.Role(b.Profile, pal, b.Theme.Variant(pal).Accent, pal.Accent, color.ContrastText, false)
	if ok {
		look.keys = jsonDimming(b.Profile, top, pal.Background)
	}
	return look
}

// jsonDimming fades the top color toward bg with depth, down to
// [contrastText] since keys are words. The top level renders top as given,
// so an explicit accent stays literal. Shades are judged as displayed on p:
// one that is not strictly dimmer than the previous is dropped, so a
// destination with a coarse palette gets fewer levels, never a reversed one.
func jsonDimming(p color.Profile, top, bg color.SRGB) jsonShades {
	out := jsonShades{p.Fg(top)}
	prev := p.Shown(top).Contrast(bg)
	hi := top.Contrast(bg)
	for i := 1; i < jsonLevels; i++ {
		target := hi - (hi-color.ContrastText)*float64(i)/float64(jsonLevels-1)
		c, ok := p.Readable(top.Quieter(bg, target), bg, color.ContrastText)
		if !ok {
			continue
		}
		shown := p.Shown(c).Contrast(bg)
		if shown >= prev {
			continue
		}
		prev = shown
		out = append(out, p.Fg(c))
	}
	return out
}

func (s *jsonLook) recolor(identBuf, w *bytes.Buffer) {
	pretty := identBuf.Bytes()
	stack := []jsonState{jsonRoot}
	lo, hi, depth := 0, identBuf.Len(), -1
	for lo < hi {
		ch := pretty[lo]
		lo++
		depth, stack = s.prettyJsonLoop(stack, w, ch, depth)
	}
	if stack[len(stack)-1] == jsonConst { // a root scalar runs to the end
		w.WriteString(ansi.Reset)
	}
}

func (s *jsonLook) prettyJsonLoop(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	curr := stack[len(stack)-1]
	if curr == jsonEscape {
		w.WriteByte(ch)
		stack = stack[:len(stack)-1]
		return depth, stack
	}
	if curr == jsonConst {
		if !s.jsonEndsConst(ch) {
			w.WriteByte(ch)
			return depth, stack
		}
		w.WriteString(ansi.Reset)
		stack = stack[:len(stack)-1]
	}
	if curr == jsonQuoted && ch == '\\' {
		return depth, s.pretttJsonBackslash(stack, w, ch)
	}
	if curr == jsonQuoted && ch != '"' {
		w.WriteByte(ch)
		return depth, stack
	}
	return s.prettyJsonChar(stack, w, ch, depth)
}

func (s *jsonLook) prettyJsonChar(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	switch ch {
	case '{', '[':
		depth, stack = s.prettyJsonOpen(stack, w, ch, depth)
	case '}', ']':
		depth, stack = s.prettyJsonClose(stack, w, ch, depth)
	case ':':
		stack = s.prettyJsonColon(stack, w, ch)
	case ',':
		stack = s.prettyJsonComma(stack, w, ch)
	case '\\':
		stack = s.pretttJsonBackslash(stack, w, ch)
	case '"':
		stack = s.prettyJsonQuote(stack, w, ch, depth)
	default:
		stack = s.prettyJsonOther(stack, w, ch)
	}
	return depth, stack
}

// jsonEndsConst reports whether ch terminates a number or literal.
func (s *jsonLook) jsonEndsConst(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', ',', ']', '}':
		return true
	}
	return false
}

// prettyJsonOther opens a number or literal in value position, and passes
// whitespace through.
func (s *jsonLook) prettyJsonOther(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	if s.jsonEndsConst(ch) {
		w.WriteByte(ch)
		return stack
	}
	switch stack[len(stack)-1] {
	case jsonValue:
		stack = stack[:len(stack)-1]
		fallthrough
	case jsonRoot, jsonArray:
		w.Write(s.value)
		stack = append(stack, jsonConst)
	default:
	}
	w.WriteByte(ch)
	return stack
}

// prettyJsonOpen enters the object or array that ch opens.
func (s *jsonLook) prettyJsonOpen(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	state := jsonObject
	if ch == '[' {
		state = jsonArray
	}
	w.Write(s.punct)
	w.WriteByte(ch)
	w.WriteString(ansi.Reset)
	return depth + 1, append(stack, state)
}

func (s *jsonLook) prettyJsonClose(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	// a pending value (number, literal or nested container) sits above its container
	if stack[len(stack)-1] == jsonValue {
		stack = stack[:len(stack)-1]
	}
	if top := stack[len(stack)-1]; top == jsonObject || top == jsonArray {
		stack = stack[:len(stack)-1]
		depth--
	}
	w.Write(s.punct)
	w.WriteByte(ch)
	w.WriteString(ansi.Reset)
	return depth, stack
}

func (s *jsonLook) prettyJsonColon(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	curr := stack[len(stack)-1]
	if curr == jsonObject {
		stack = append(stack, jsonValue)
	}
	w.WriteByte(ch)
	return stack
}

func (s *jsonLook) prettyJsonComma(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	curr := stack[len(stack)-1]
	if curr == jsonValue {
		stack = stack[:len(stack)-1]
	}
	w.WriteByte(ch)
	return stack
}

func (s *jsonLook) pretttJsonBackslash(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	stack = append(stack, jsonEscape)
	w.WriteByte(ch)
	return stack
}

func (s *jsonLook) prettyJsonQuote(stack []jsonState, w *bytes.Buffer, ch byte, depth int) []jsonState {
	curr := stack[len(stack)-1]
	switch curr {
	case jsonObject: // open key
		w.Write(s.keys.at(depth))
		w.WriteString(ansi.Bold)
		w.WriteByte(ch)
		stack = append(stack, jsonQuoted)
	case jsonQuoted: // close key or string
		w.WriteByte(ch)
		w.WriteString(ansi.Reset)
		stack = stack[:len(stack)-1]
	case jsonValue: // open string
		stack = stack[:len(stack)-1]
		fallthrough
	case jsonRoot, jsonArray: // open string
		w.Write(s.value)
		w.WriteByte(ch)
		stack = append(stack, jsonQuoted)
	default:
		w.WriteByte(ch)
	}
	return stack
}
