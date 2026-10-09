// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

var keyDepthShades = []string{ // shades of blue
	"\x1b[38;5;27m",
	"\x1b[38;5;33m",
	"\x1b[38;5;39m",
	"\x1b[38;5;45m",
	"\x1b[38;5;51m",
}

var valDepthShades = []string{ // shades of green
	"\x1b[38;5;46m",
	"\x1b[38;5;40m",
	"\x1b[38;5;34m",
	"\x1b[38;5;28m",
	"\x1b[38;5;22m",
}

// PrettyJSON pretty-prints JSON data with depth-aware coloring.
func PrettyJSON(w io.Writer, src any) error {
	identBuf, err := jsonIndent(src)
	if err != nil {
		return err
	}
	if !isTerminal() {
		_, err = identBuf.WriteTo(w)
		return err
	}
	outBuf := bytes.NewBuffer(nil)
	prettyJsonRecolor(identBuf, outBuf)
	_, err = outBuf.WriteTo(w)
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

func prettyJsonRecolor(identBuf, w *bytes.Buffer) {
	pretty := identBuf.Bytes()
	stack := []jsonState{jsonRoot}
	lo, hi, depth := 0, identBuf.Len(), -1
	for lo < hi {
		ch := pretty[lo]
		lo++
		depth, stack = prettyJsonLoop(stack, w, ch, depth)
	}
	w.WriteByte('\n')
}

func prettyJsonLoop(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	curr := stack[len(stack)-1]
	if curr == jsonEscape {
		w.WriteByte(ch)
		stack = stack[:len(stack)-1]
		return depth, stack
	}
	if curr == jsonQuoted && ch == '\\' {
		return depth, pretttJsonBackslash(stack, w, ch)
	}
	if curr == jsonQuoted && ch != '"' {
		w.WriteByte(ch)
		return depth, stack
	}
	return prettyJsonChar(stack, w, ch, depth)
}

func prettyJsonChar(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	switch ch {
	case '{':
		depth, stack = prettyJsonOpenObject(stack, w, ch, depth)
	case '[':
		depth, stack = prettyJsonOpenArray(stack, w, ch, depth)
	case '}', ']':
		depth, stack = prettyJsonClose(stack, w, ch, depth)
	case ':':
		stack = prettyJsonColon(stack, w, ch)
	case ',':
		stack = prettyJsonComma(stack, w, ch)
	case '\\':
		stack = pretttJsonBackslash(stack, w, ch)
	case '"':
		stack = prettyJsonQuote(stack, w, ch, depth)
	default:
		w.WriteByte(ch)
	}
	return depth, stack
}

func prettyJsonOpenObject(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	depth++
	stack = append(stack, jsonObject)
	w.WriteString(bold)
	w.WriteByte(ch)
	w.WriteString(reset)
	return depth, stack
}

func prettyJsonOpenArray(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	depth++
	stack = append(stack, jsonArray)
	w.WriteString(bold)
	w.WriteByte(ch)
	w.WriteString(reset)
	return depth, stack
}

func prettyJsonClose(stack []jsonState, w *bytes.Buffer, ch byte, depth int) (int, []jsonState) {
	// a pending value (number, literal or nested container) sits above its container
	if stack[len(stack)-1] == jsonValue {
		stack = stack[:len(stack)-1]
	}
	if top := stack[len(stack)-1]; top == jsonObject || top == jsonArray {
		stack = stack[:len(stack)-1]
		depth--
	}
	w.WriteString(bold)
	w.WriteByte(ch)
	w.WriteString(reset)
	return depth, stack
}

func prettyJsonColon(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	curr := stack[len(stack)-1]
	if curr == jsonObject {
		stack = append(stack, jsonValue)
	}
	w.WriteByte(ch)
	return stack
}

func prettyJsonComma(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	curr := stack[len(stack)-1]
	if curr == jsonValue {
		stack = stack[:len(stack)-1]
	}
	w.WriteByte(ch)
	return stack
}

func pretttJsonBackslash(stack []jsonState, w *bytes.Buffer, ch byte) []jsonState {
	stack = append(stack, jsonEscape)
	w.WriteByte(ch)
	return stack
}

func prettyJsonQuote(stack []jsonState, w *bytes.Buffer, ch byte, depth int) []jsonState {
	curr := stack[len(stack)-1]
	switch curr {
	case jsonObject: // open key
		w.WriteString(keyDepthShades[abs(depth)%len(keyDepthShades)])
		w.WriteString(bold)
		w.WriteByte(ch)
		stack = append(stack, jsonQuoted)
	case jsonQuoted: // close key
		w.WriteByte(ch)
		w.WriteString(reset)
		stack = stack[:len(stack)-1]
	case jsonRoot, jsonArray: // open const
		w.WriteString(valDepthShades[abs(depth)%len(valDepthShades)])
		w.WriteByte(ch)
		stack = append(stack, jsonQuoted)
	case jsonValue: // open const
		w.WriteString(valDepthShades[abs(depth)%len(valDepthShades)])
		w.WriteByte(ch)
		stack = stack[:len(stack)-1]
		stack = append(stack, jsonQuoted)
	default:
		w.WriteByte(ch)
	}
	return stack
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
