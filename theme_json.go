// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
type jsonShades []text

// at returns the shade for depth, brightest at the top level.
func (s jsonShades) at(depth int) text {
	if len(s) == 0 {
		return nil
	}
	return s[min(max(depth, 0), len(s)-1)]
}

// jsonLook is the encoded presentation of JSON tokens for one destination
// that accepts SGR. Keys are always bold, after their shade.
type jsonLook struct {
	keys  jsonShades // may be empty
	value text       // prefix for strings, numbers and literals
	punct text       // prefix for brackets and braces
}

// newJSONLook maps the roles of a binding onto JSON: keys are the accent,
// dimmed with depth, values the foreground and punctuation the muted role.
// A neutral role that keeps the terminal default, as on an unknown
// background where it could be nearly invisible, leaves values unstyled and
// makes punctuation bold. It is computed per operation: the theme, its
// background and the destination all decide it.
func newJSONLook(b binding) jsonLook {
	look := jsonLook{value: b.roles.foreground, punct: b.roles.muted}
	if len(look.punct) == 0 {
		look.punct = text(bold)
	}
	if b.profile < profileANSI16 {
		return look
	}
	pal := b.palette
	top, ok := b.theme.role(b.profile, pal, b.theme.variant(pal).accent, pal.Accent, contrastText, false)
	if ok {
		look.keys = jsonDimming(b.profile, top, pal.Background)
	}
	return look
}

// jsonDimming fades the top color toward bg with depth, down to
// [contrastText] since keys are words. The top level renders top as given,
// so an explicit accent stays literal. Shades are judged as displayed on p:
// one that is not strictly dimmer than the previous is dropped, so a
// destination with a coarse palette gets fewer levels, never a reversed one.
func jsonDimming(p colorProfile, top, bg sRGB) jsonShades {
	out := jsonShades{p.fg(top)}
	prev := p.shown(top).contrast(bg)
	hi := top.contrast(bg)
	for i := 1; i < jsonLevels; i++ {
		target := hi - (hi-contrastText)*float64(i)/float64(jsonLevels-1)
		c, ok := p.readable(top.quieter(bg, target), bg, contrastText)
		if !ok {
			continue
		}
		shown := p.shown(c).contrast(bg)
		if shown >= prev {
			continue
		}
		prev = shown
		out = append(out, p.fg(c))
	}
	return out
}

// JSONStyle is the layout of [PrettyJSON].
type JSONStyle struct {
	// Indent is one level of indentation: at most [maxJSONIndent] spaces or
	// tabs, so that the output stays valid JSON. Empty takes two spaces.
	Indent string
	// Compact prints the JSON on one line, without insignificant
	// whitespace, ignoring Indent.
	Compact bool
}

// jsonStyle is the theme preset: the legacy two-space indentation.
var jsonStyle = JSONStyle{Indent: "  "}

// maxJSONIndent bounds the bytes of one indentation level.
const maxJSONIndent = 16

// normalize fills the preset and rejects indentation that is not whitespace.
func (s JSONStyle) normalize() (JSONStyle, error) {
	if s.Indent == "" {
		s.Indent = jsonStyle.Indent
	}
	if len(s.Indent) > maxJSONIndent || strings.Trim(s.Indent, " \t") != "" {
		return s, fmt.Errorf("theme.JSON.Indent: %q: want at most %d spaces or tabs", s.Indent, maxJSONIndent)
	}
	return s, nil
}

// PrettyJSON pretty-prints JSON data, with two-space indentation unless the
// theme sets another [JSONStyle]. When the destination writer accepts color,
// keys use the accent color, dimmed with nesting depth, values the
// foreground and brackets a muted shade. Color follows the writer and the
// NO_COLOR, FORCE_COLOR and CLICOLOR_FORCE conventions, never os.Stdout, and
// the terminal is never queried. Without color the output is plain JSON.
// Malformed input returns an error before anything is written.
func PrettyJSON(w io.Writer, src any) error {
	theme := defaultIO.currentTheme()
	out, err := jsonIndent(src, theme.source.JSON)
	if err != nil {
		return err
	}
	if p := defaultIO.profileFor(w); p.attrs() {
		style := newJSONLook(defaultIO.bindTheme(context.Background(), theme, w, nil))
		colored := bytes.NewBuffer(make([]byte, 0, 2*out.Len()))
		style.recolor(out, colored)
		out = colored
	}
	out.WriteByte('\n')
	_, err = out.WriteTo(w)
	return err
}

// jsonIndent validates src and lays it out in style, without a trailing newline.
func jsonIndent(src any, style JSONStyle) (*bytes.Buffer, error) {
	identBuf := bytes.NewBuffer(nil)
	var toIndent []byte
	var err error
	switch src := src.(type) {
	case string:
		toIndent = []byte(src)
	case []byte:
		toIndent = src
	default:
		toIndent, err = json.Marshal(src)
		if err != nil {
			return nil, fmt.Errorf("marshal: %w", err)
		}
	}
	// standard library already indents JSON data, so we're only coloring things here.
	if style.Compact {
		err = json.Compact(identBuf, toIndent)
	} else {
		err = json.Indent(identBuf, toIndent, "", style.Indent)
	}
	if err != nil {
		return nil, fmt.Errorf("indent: %w", err)
	}
	return identBuf, nil
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
		w.WriteString(reset)
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
		w.WriteString(reset)
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
	w.WriteString(reset)
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
	w.WriteString(reset)
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
		w.WriteString(bold)
		w.WriteByte(ch)
		stack = append(stack, jsonQuoted)
	case jsonQuoted: // close key or string
		w.WriteByte(ch)
		w.WriteString(reset)
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
