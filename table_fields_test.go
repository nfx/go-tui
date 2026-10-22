// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestStructFieldsTemplate(t *testing.T) {
	fields := structFields{
		&fieldMetadata{name: "Name", kind: reflect.String},
		&fieldMetadata{name: "Age", kind: reflect.Int},
	}
	assert.Equal(t, "{{.Name}}\t{{.Age}}", fields.Template())
}

func TestStructFieldsForInvalid(t *testing.T) {
	_, err := structFieldsForType(reflect.TypeFor[int]())
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestStructFieldsForNested(t *testing.T) {
	type inner struct {
		Value int
		Flag  bool `header:"state,align-left"`
	}
	type data struct {
		Name  string
		Inner inner
		Score int `header:"score,align-right"`
	}
	fields, err := structFieldsForType(reflect.TypeFor[data]())
	assert.NoError(t, err)
	assert.True(t, len(fields) >= 3)
}

func TestStructFieldsForRecursiveType(t *testing.T) {
	type node struct {
		Name string
		Next *node
	}

	fields, err := structFieldsForType(reflect.TypeFor[node]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(fields))
	assert.Equal(t, "Name", fields[0].name)
	assert.Equal(t, "NAME", fields[0].header)
}

func TestStructFieldsForIndirectRecursiveType(t *testing.T) {
	fields, err := structFieldsForType(reflect.TypeFor[recursiveLeft]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(fields))
	assert.Equal(t, "Value", fields[0].name)
	assert.Equal(t, "VALUE", fields[0].header)
}

func TestStructFieldsForStringerStruct(t *testing.T) {
	type row struct {
		Status pointerStringerStruct
	}
	fields, err := structFieldsForType(reflect.TypeFor[row]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(fields))
	assert.Equal(t, "Status", fields[0].name)
	assert.Equal(t, "STATUS", fields[0].header)
}

func TestTableFieldValueWithTabDoesNotPanic(t *testing.T) {
	buf := &bytes.Buffer{}
	type item struct {
		Name string
		ID   string
	}
	// a tab inside a field value must not panic with index out of range
	data := []item{{Name: "foo\tbar", ID: "123"}, {Name: "baz", ID: "456"}}
	assert.NoError(t, TableAuto(buf, data))
}

func TestTableFieldTabKeepsCellIdentity(t *testing.T) {
	type item struct {
		Name string
		ID   string
	}
	data := []item{{Name: "foo\tbar", ID: "123"}, {Name: "baz", ID: "456"}}
	for _, tmpl := range []string{"", "{{.Name}}\t{{.ID}}"} {
		events := collectEvents(t, tmpl, data)
		first := mustTableRow(t, events[1]).Cells
		assert.Equal(t, "foo bar", strings.TrimSpace(first[0]))
		assert.Equal(t, "123", strings.TrimSpace(first[1]))
		second := mustTableRow(t, events[2]).Cells
		assert.Equal(t, "baz", strings.TrimSpace(second[0]))
		assert.Equal(t, "456", strings.TrimSpace(second[1]))
	}
}
