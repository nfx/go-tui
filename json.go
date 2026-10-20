// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sync"
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

// jsonStyle is the encoded presentation of JSON tokens for one destination
// that accepts SGR. Keys are always bold, after their shade.
type jsonStyle struct {
	keys  jsonShades // may be empty
	value text       // prefix for strings, numbers and literals
	punct text       // prefix for brackets and braces
}

// newJSONStyle maps the resolved palette onto JSON: keys are Accent, dimmed
// with depth, values are Foreground and punctuation is Muted.
func newJSONStyle(p colorProfile, pal resolvedPalette) jsonStyle {
	if p < profileANSI16 {
		return jsonStyle{punct: text(bold)}
	}
	return jsonStyle{
		keys:  jsonDimming(p, pal.Accent, pal.Background),
		value: p.fg(pal.Foreground),
		punct: p.fg(pal.Muted),
	}
}

// jsonDimming fades the top color toward bg with depth, down to
// [contrastText] since keys are words. Shades are judged as displayed on p:
// one that is not strictly dimmer than the previous is dropped, so a
// destination with a coarse palette gets fewer levels, never a reversed one.
func jsonDimming(p colorProfile, top, bg sRGB) jsonShades {
	hi := top.contrast(bg)
	var out jsonShades
	prev := math.Inf(1)
	for i := range jsonLevels {
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

// jsonPalette is the palette PrettyJSON renders with. The terminal
// appearance is not measured for plain writers yet, so the dark variant's
// reference background is assumed.
var jsonPalette = sync.OnceValue(func() resolvedPalette {
	pal, err := Palette{}.resolve(false, sRGB{}, false)
	if err != nil {
		panic(err) // the preset is valid
	}
	return pal
})

// jsonStyles caches the style of each profile: it depends on nothing else,
// and deriving it takes thousands of color conversions.
var jsonStyles = func() (out [profileTrueColor + 1]func() jsonStyle) {
	for p := range out {
		out[p] = sync.OnceValue(func() jsonStyle { return newJSONStyle(colorProfile(p), jsonPalette()) })
	}
	return out
}()

// PrettyJSON pretty-prints JSON data with two-space indentation. When the
// destination writer accepts color, keys use the accent color, dimmed with
// nesting depth, values the foreground and brackets a muted shade. Color follows the writer and the
// NO_COLOR, FORCE_COLOR and CLICOLOR_FORCE conventions, never os.Stdout.
// Without color the output is plain indented JSON.
func PrettyJSON(w io.Writer, src any) error {
	out, err := jsonIndent(src)
	if err != nil {
		return err
	}
	if profile := defaultIO.profileFor(w); profile.attrs() {
		style := jsonStyles[profile]()
		colored := bytes.NewBuffer(make([]byte, 0, 2*out.Len()))
		style.recolor(out, colored)
		out = colored
	}
	out.WriteByte('\n')
	_, err = out.WriteTo(w)
	return err
}

func jsonIndent(src any) (*bytes.Buffer, error) {
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
	err = json.Indent(identBuf, toIndent, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("indent: %w", err)
	}
	return identBuf, nil
}

func (s *jsonStyle) recolor(identBuf, w *bytes.Buffer) {
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

func (s *jsonStyle) prettyJsonLoop(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
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

func (s *jsonStyle) prettyJsonChar(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
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
func (s *jsonStyle) jsonEndsConst(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', ',', ']', '}':
		return true
	}
	return false
}

// prettyJsonOther opens a number or literal in value position, and passes
// whitespace through.
func (s *jsonStyle) prettyJsonOther(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
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
func (s *jsonStyle) prettyJsonOpen(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	state := jsonObject
	if ch == '[' {
		state = jsonArray
	}
	w.Write(s.punct)
	w.WriteByte(ch)
	w.WriteString(reset)
	return depth + 1, append(stack, state)
}

func (s *jsonStyle) prettyJsonClose(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
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

func (s *jsonStyle) prettyJsonColon(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	curr := stack[len(stack)-1]
	if curr == jsonObject {
		stack = append(stack, jsonValue)
	}
	w.WriteByte(ch)
	return stack
}

func (s *jsonStyle) prettyJsonComma(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	curr := stack[len(stack)-1]
	if curr == jsonValue {
		stack = stack[:len(stack)-1]
	}
	w.WriteByte(ch)
	return stack
}

func (s *jsonStyle) pretttJsonBackslash(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	stack = append(stack, jsonEscape)
	w.WriteByte(ch)
	return stack
}

func (s *jsonStyle) prettyJsonQuote(stack []jsonState, w *bytes.Buffer, ch byte, depth int) []jsonState {
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
