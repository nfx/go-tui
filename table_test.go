// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"io"
	"iter"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/assert"
)

type Pet struct {
	Name  string
	Age   int
	Type  string
	Owner Person
}

type Person struct {
	Name string
	Age  int `header:"Owner Age,align-left"`
}

var dummyPets = []Pet{
	{"John Doe", 99, "", Person{"Unknown", 9999}},
	{"Fluffy", 3, "Cat", Person{"Alice", 30}},
	{"Buddy", 12, "Dog", Person{"Bob", 25}},
	{"Goldie", 1, "Fish", Person{"Charlie", 20}},
	{"Tweety", 2, "Bird", Person{"Diana", 35}},
	{"Nemo", 1, "Fish", Person{"Eve", 9}},
	{"Max", 4, "Dog", Person{"Frank", 40}},
	{"Whiskers", 2, "Cat", Person{"Grace", 22}},
}

func iterate[T any](items []T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for _, v := range items {
			if !yield(v, nil) {
				return
			}
		}
	}
}

func TestTableRender(t *testing.T) {
	buf := &bytes.Buffer{}
	err := TableIter(buf, "{{ bold .Name | green }}\t{{ .Age }}\t{{ .Type }}\t{{ .Owner.Name }}\t{{ .Owner.Age }}", iterate(dummyPets))
	assert.NoError(t, err)
}

func TestTableEmitsStructuredEvents(t *testing.T) {
	type row struct {
		Name string
		Age  int
	}
	data := []row{
		{Name: "Alice", Age: 30},
		{Name: "Bob", Age: 25},
	}
	buf := &bytes.Buffer{}
	var events []tableEvent
	err := Table(buf, "{{.Name}}\t{{.Age}}", data, opT(func(tbl *table) error {
		tbl.eventSink = func(ev tableEvent) {
			events = append(events, ev)
		}
		return nil
	}))
	assert.NoError(t, err)
	assert.Equal(t, "", buf.String())
	assert.Equal(t, 4, len(events))

	begin, ok := events[0].(tableBegin)
	assert.True(t, ok)
	assert.Equal(t, []tableColumnInfo{
		{Header: "NAME", Kind: "string"},
		{Header: "AGE", Kind: "int"},
	}, begin.Columns)

	first, ok := events[1].(tableRow)
	assert.True(t, ok)
	assert.Equal(t, []string{"Alice", "30"}, []string{
		strings.TrimSpace(first.Cells[0]),
		strings.TrimSpace(first.Cells[1]),
	})

	second, ok := events[2].(tableRow)
	assert.True(t, ok)
	assert.Equal(t, []string{"Bob", "25"}, []string{
		strings.TrimSpace(second.Cells[0]),
		strings.TrimSpace(second.Cells[1]),
	})

	end, ok := events[3].(tableEnd)
	assert.True(t, ok)
	assert.Equal(t, 2, end.Rows)
}

func TestTableIterFetchError(t *testing.T) {
	iter := func(yield func(Person, error) bool) {
		var zero Person
		yield(zero, io.EOF)
	}
	err := TableIter(&bytes.Buffer{}, "{{.Name}}", iter)
	assert.Error(t, err)
}

func TestTableIterAppendError(t *testing.T) {
	iter := func(yield func(Person, error) bool) {
		yield(Person{Name: "A"}, nil)
	}
	err := TableIter(errWriterTable{}, "{{.Name}}", iter, opT(func(t *table) error {
		t.batchSize = 1
		return nil
	}))
	assert.Error(t, err)
}

func TestTableExtractFromNode(t *testing.T) {
	tbl := &table{}
	cases := []struct {
		name    string
		tmpl    string
		want    []string
		wantErr string
	}{
		{name: "field", tmpl: "{{.Name}}", want: []string{"Name"}},
		{name: "nested field", tmpl: "{{.Owner.Name}}", want: []string{"Owner.Name"}},
		{name: "dot", tmpl: "{{.}}", want: []string{"."}},
		{name: "var", tmpl: "{{$x := .Name}}{{$x}}", want: []string{"Name", "$x"}},
		{name: "number", tmpl: "{{123}}", want: nil},
		{name: "string", tmpl: "{{\"hi\"}}", want: nil},
		{name: "if", tmpl: "{{if .Ok}}x{{end}}", want: []string{"Ok"}},
		{name: "pipe", tmpl: "{{.Name | printf \"%s\"}}", want: []string{"Name"}},
		{name: "range", tmpl: "{{range .Items}}{{.}}{{end}}", wantErr: "range is not supported"},
		{name: "with", tmpl: "{{with .Item}}{{.}}{{end}}", wantErr: "with is not supported"},
		{name: "template", tmpl: "{{template \"x\" .}}", wantErr: "template is not supported"},
		{name: "chain", tmpl: "{{.A.B.C | printf \"%s\"}}", want: []string{"A.B.C"}},
		{name: "multi args", tmpl: "{{printf \"%s\" .Name .Age}}", wantErr: "multiple arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := template.New("t").Parse(tc.tmpl)
			assert.NoError(t, err)
			got, err := tbl.extractFromNode(tmpl.Root)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error %q, got %v", tc.wantErr, err)
				}
				return
			}
			assert.NoError(t, err)
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("expected no headers, got %v", got)
				}
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFieldMetadataTemplate(t *testing.T) {
	boolField := &fieldMetadata{name: "Enabled", kind: reflect.Bool}
	assert.Equal(t, "{{if .Enabled}}yes{{else}}no{{end}}", boolField.Template())
	floatField := &fieldMetadata{name: "Rate", kind: reflect.Float64}
	assert.Equal(t, "{{printf \"%.2f\" .Rate}}", floatField.Template())
	stringField := &fieldMetadata{name: "Name", kind: reflect.String}
	assert.Equal(t, "{{.Name}}", stringField.Template())
}

func TestStructFieldsTemplate(t *testing.T) {
	fields := structFields{
		&fieldMetadata{name: "Name", kind: reflect.String},
		&fieldMetadata{name: "Age", kind: reflect.Int},
	}
	assert.Equal(t, "{{.Name}}\t{{.Age}}", fields.Template())
}

func TestStructFieldsForInvalid(t *testing.T) {
	_, err := structFieldsFor[int]()
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestTableAppendFlushes(t *testing.T) {
	buf := &bytes.Buffer{}
	tbl, err := newTable[Person](buf, "{{.Name}}\t{{.Age}}")
	assert.NoError(t, err)
	tbl.batchSize = 1
	err = tbl.Append(Person{Name: "A", Age: 1})
	assert.NoError(t, err)
	if buf.Len() == 0 {
		t.Fatalf("expected output")
	}
}

func TestNewTableInvalidTemplate(t *testing.T) {
	_, err := newTable[Person](&bytes.Buffer{}, "{{")
	assert.Error(t, err)
}

func TestTableXInvalidType(t *testing.T) {
	buf := &bytes.Buffer{}
	err := TableX(buf, []int{1, 2})
	assert.Error(t, err)
}

func TestTableInvalidTemplate(t *testing.T) {
	buf := &bytes.Buffer{}
	err := Table(buf, "{{", []Person{{Name: "A"}})
	assert.Error(t, err)
}

type errWriterTable struct{}

func (errWriterTable) Write(p []byte) (int, error) {
	return 0, io.EOF
}

func TestTableAppendWriteError(t *testing.T) {
	tbl, err := newTable[Person](errWriterTable{}, "{{.Name}}")
	assert.NoError(t, err)
	tbl.batchSize = 1
	err = tbl.Append(Person{Name: "A"})
	assert.Error(t, err)
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
	fields, err := structFieldsFor[data]()
	assert.NoError(t, err)
	assert.True(t, len(fields) >= 3)
}

func TestExtractFromRangeNode(t *testing.T) {
	tmpl, err := template.New("row").Parse(`{{range .Items}}{{.}}{{end}}`)
	assert.NoError(t, err)
	tr := table{tmpl: tmpl}
	_, err = tr.extractFromNode(tmpl.Root)
	assert.Error(t, err)
}

func TestTableWriterWritesHeaders(t *testing.T) {
	buf := &bytes.Buffer{}
	type item struct {
		Name string
	}
	data := []item{{Name: "a"}, {Name: "b"}}
	assert.NoError(t, Table(buf, "{{.Name}}", data))
	assert.True(t, buf.Len() > 0)
}

func TestReflectFieldMetadataUnknownOption(t *testing.T) {
	ft := reflect.TypeOf(int64(0))
	_, err := reflectFieldMetadata(ft, `header:"Value,align-diagonal"`, "Field")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown header option")
}

func TestTableXWrites(t *testing.T) {
	buf := &bytes.Buffer{}
	type item struct {
		Name string
	}
	data := []item{{Name: "a"}, {Name: "b"}}
	assert.NoError(t, TableX(buf, data, WithLabelTemplate("{{.Name}} ")))
}

func TestTableIterHandlesIteratorError(t *testing.T) {
	buf := &bytes.Buffer{}
	iter := func(yield func(int, error) bool) {
		if !yield(1, nil) {
			return
		}
		yield(0, errors.New("fail"))
	}

	assert.Error(t, TableIter(buf, "{{.}}", iter))
}
