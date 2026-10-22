// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
)

// choiceLabels owns the shared label contract without widget interaction state.
type choiceLabels struct{}

// itemLabel gives choice widgets a consistent label for display and search.
func (d choiceLabels) itemLabel(item any) string {
	label, ok := d.structLabel(item)
	if ok {
		return label
	}
	label, ok = d.stringerLabel(item)
	if ok {
		return label
	}
	return fmt.Sprint(item)
}

// rowLabel flattens item labels to fit single-line choice slots.
func (d choiceLabels) rowLabel(item any) string { return text(d.itemLabel(item)).oneLine() }

// plainText removes terminal styling and preserves word boundaries for ranking.
func (choiceLabels) plainText(t text) string {
	var b strings.Builder
	for seg := range t.segments() {
		if seg.isText() {
			b.Write(seg.text)
		} else if bytes.Equal(seg.text, []byte{'\n'}) || bytes.Equal(seg.text, []byte{'\t'}) {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// structLabel extracts a label from struct items using tags and common field names.
func (d choiceLabels) structLabel(item any) (string, bool) {
	value, ok := d.indirectValue(reflect.ValueOf(item))
	if !ok || value.Kind() != reflect.Struct {
		return "", false
	}
	label, ok := d.annotatedStructLabel(value)
	if ok {
		return label, true
	}
	return d.heuristicStructLabel(value)
}

// stringerLabel returns String() output, including pointer-receiver methods on struct values.
func (d choiceLabels) stringerLabel(item any) (string, bool) {
	x, ok := item.(fmt.Stringer)
	if ok {
		return x.String(), true
	}
	value, ok := d.indirectValue(reflect.ValueOf(item))
	if !ok || value.Kind() != reflect.Struct {
		return "", false
	}
	ptr := reflect.New(value.Type())
	ptr.Elem().Set(value)
	x, ok = ptr.Interface().(fmt.Stringer)
	if !ok {
		return "", false
	}
	return x.String(), true
}

// annotatedStructLabel resolves fields explicitly marked as label.
func (d choiceLabels) annotatedStructLabel(value reflect.Value) (string, bool) {
	rt := value.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		if !field.IsExported() || !d.isLabelTag(field.Tag) {
			continue
		}
		label, ok := d.valueLabel(value.Field(i))
		if ok {
			return label, true
		}
	}
	return "", false
}

// heuristicStructLabel resolves labels from common field names.
func (d choiceLabels) heuristicStructLabel(value reflect.Value) (string, bool) {
	rt := value.Type()
	for _, want := range []string{
		"label",
		"name",
		"title",
		"description",
		"displayname",
		"fullname",
		"summary",
		"text",
		"subject",
	} {
		for i := range rt.NumField() {
			field := rt.Field(i)
			if !field.IsExported() || !strings.EqualFold(field.Name, want) {
				continue
			}
			label, ok := d.valueLabel(value.Field(i))
			if ok {
				return label, true
			}
		}
	}
	return "", false
}

// valueLabel stringifies an annotated or heuristic field and skips empty values.
func (d choiceLabels) valueLabel(value reflect.Value) (string, bool) {
	value, ok := d.indirectValue(value)
	if !ok {
		return "", false
	}
	if value.Kind() == reflect.String {
		out := strings.TrimSpace(value.String())
		if out != "" {
			return out, true
		}
		return "", false
	}
	if value.CanInterface() {
		x, ok := value.Interface().(fmt.Stringer)
		if ok {
			out := strings.TrimSpace(x.String())
			if out != "" {
				return out, true
			}
			return "", false
		}
	}
	if value.Kind() == reflect.Struct {
		ptr := reflect.New(value.Type())
		ptr.Elem().Set(value)
		x, ok := ptr.Interface().(fmt.Stringer)
		if ok {
			out := strings.TrimSpace(x.String())
			if out != "" {
				return out, true
			}
			return "", false
		}
	}
	return "", false
}

// isLabelTag recognizes explicit label annotations shared by choice widgets.
func (d choiceLabels) isLabelTag(tag reflect.StructTag) bool {
	return strings.EqualFold(strings.TrimSpace(tag.Get("header")), "label") ||
		d.hasLabelOption(tag.Get("tui")) ||
		strings.EqualFold(strings.TrimSpace(tag.Get("label")), "true")
}

// hasLabelOption checks whether a comma-separated tag contains "label".
func (d choiceLabels) hasLabelOption(tag string) bool {
	for option := range strings.SplitSeq(tag, ",") {
		if strings.EqualFold(strings.TrimSpace(option), "label") {
			return true
		}
	}
	return false
}

// indirectValue dereferences interface/pointer wrappers and rejects nil values.
func (d choiceLabels) indirectValue(value reflect.Value) (reflect.Value, bool) {
	if !value.IsValid() {
		return reflect.Value{}, false
	}
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	return value, true
}
