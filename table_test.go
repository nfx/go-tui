// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"reflect"
	"strings"
	"testing"
	"text/template"
	"time"

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

type recursiveLeft struct {
	Value int
	Right *recursiveRight
}

type recursiveRight struct {
	Left *recursiveLeft
}

type pointerStringerStruct struct {
	Value string
}

func (s *pointerStringerStruct) String() string {
	return "stringer:" + s.Value
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
	err := TableIter(
		buf,
		"{{ bold .Name | green }}\t{{ .Age }}\t{{ .Type }}\t{{ .Owner.Name }}\t{{ .Owner.Age }}",
		iterate(dummyPets),
	)
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
	_, err := structFieldsForType(reflect.TypeFor[int]())
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestTableAppendFlushes(t *testing.T) {
	buf := &bytes.Buffer{}
	tbl, err := newTable[Person](buf, "{{.Name}}\t{{.Age}}", false)
	assert.NoError(t, err)
	tbl.batchSize = 1
	err = tbl.Append(Person{Name: "A", Age: 1})
	assert.NoError(t, err)
	if buf.Len() == 0 {
		t.Fatalf("expected output")
	}
}

func TestNewTableInvalidTemplate(t *testing.T) {
	_, err := newTable[Person](&bytes.Buffer{}, "{{", false)
	assert.Error(t, err)
}

func TestTableXInvalidType(t *testing.T) {
	buf := &bytes.Buffer{}
	err := TableAuto(buf, []int{1, 2})
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
	tbl, err := newTable[Person](errWriterTable{}, "{{.Name}}", false)
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
	assert.NoError(t, TableAuto(buf, data))
	assert.True(t, buf.Len() > 0)
}

func TestTableRejectsOptionsOfOtherWidgets(t *testing.T) {
	type item struct {
		Name string
	}
	data := []item{{Name: "a"}}
	err := TableAuto(&bytes.Buffer{}, data, WithLabelTemplate("{{.Name}} "))
	assert.ErrorIs(t, err, ErrWrongWidget)
	assert.Contains(t, err.Error(), "need tui.withPrompt, got *tui.table")

	err = TableAuto(&bytes.Buffer{}, data, WithOutput(&bytes.Buffer{}))
	assert.ErrorIs(t, err, ErrWrongWidget)
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

type tableFormatRow struct {
	Name   string
	Rate   float64
	Amount int
}

// collectEvents runs a table render and returns emitted table events.
func collectEvents[T any](t *testing.T, rowTmpl string, data []T, o ...opt) []tableEvent {
	t.Helper()
	var events []tableEvent
	o = append(o, opT(func(tbl *table) error {
		tbl.eventSink = func(ev tableEvent) {
			events = append(events, ev)
		}
		return nil
	}))
	err := Table(bytes.NewBuffer(nil), rowTmpl, data, o...)
	assert.NoError(t, err)
	return events
}

func collectIterEvents[T any](t *testing.T, rowTmpl string, data []T, o ...opt) []tableEvent {
	t.Helper()
	var events []tableEvent
	o = append(o, opT(func(tbl *table) error {
		tbl.eventSink = func(ev tableEvent) {
			events = append(events, ev)
		}
		return nil
	}))
	err := TableIter(bytes.NewBuffer(nil), rowTmpl, iterate(data), o...)
	assert.NoError(t, err)
	return events
}

// mustTableRow converts an event to a tableRow and fails the test when the type is unexpected.
func mustTableRow(t *testing.T, ev tableEvent) tableRow {
	t.Helper()
	row, ok := ev.(tableRow)
	assert.True(t, ok)
	return row
}

func assertNoGreenRedScaleColor(t *testing.T, cell string) {
	t.Helper()
	assert.NotContains(t, cell, brightRed)
	assert.NotContains(t, cell, red)
	assert.NotContains(t, cell, yellow)
	assert.NotContains(t, cell, green)
	assert.NotContains(t, cell, brightGreen)
}

func assertScaledCellContains(t *testing.T, cell, color, text string) {
	t.Helper()
	assert.Contains(t, cell, color)
	assert.Contains(t, cell, text)
	assert.Contains(t, cell, reset)
}

func factLines(out string) []string {
	plain := strings.NewReplacer("\x1b[1m", "", "\x1b[0m", "").Replace(out)
	raw := strings.Split(plain, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, " ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func TestWithColumnTypeFormat(t *testing.T) {
	data := []tableFormatRow{{Name: "A", Rate: 0.05873242}}
	events := collectEvents(t, "", data, WithColumnTypeFormat(func(v float64) string {
		return fmt.Sprintf("rate=%.4f", v)
	}))
	row := mustTableRow(t, events[1])
	assert.Equal(t, "rate=0.0587", strings.TrimSpace(row.Cells[1]))
}

func TestWithColumnNameFormatWinsOverType(t *testing.T) {
	data := []tableFormatRow{{Name: "A", Rate: 0.05873242}}
	events := collectEvents(t, "", data,
		WithColumnTypeFormat(func(v float64) string { return "TYPE" }),
		WithColumnFormat("Rate", func(v float64) string { return "NAME" }),
	)
	row := mustTableRow(t, events[1])
	assert.Equal(t, "NAME", strings.TrimSpace(row.Cells[1]))
}

func TestWithColumnTemplateWinsOverNameAndType(t *testing.T) {
	data := []tableFormatRow{{Name: "A", Rate: 0.05873242}}
	events := collectEvents(t, "", data,
		WithColumnTypeFormat(func(v float64) string { return "TYPE" }),
		WithColumnFormat("Rate", func(v float64) string { return "NAME" }),
		WithColumnTemplate("Rate", `{{printf "TMPL(%.1f)" .Rate}}`),
	)
	row := mustTableRow(t, events[1])
	assert.Equal(t, "TMPL(0.1)", strings.TrimSpace(row.Cells[1]))
}

func TestWithIncludeColumns(t *testing.T) {
	data := []tableFormatRow{{Name: "A", Rate: 0.1, Amount: 2}}
	events := collectEvents(t, "", data, WithIncludeColumns("Name", "Amount"))
	begin, ok := events[0].(tableBegin)
	assert.True(t, ok)
	assert.Equal(t, []tableColumnInfo{
		{Header: "NAME", Kind: "string"},
		{Header: "AMOUNT", Kind: "int"},
	}, begin.Columns)
	row, ok := events[1].(tableRow)
	assert.True(t, ok)
	assert.Equal(t, 2, len(row.Cells))
	assert.Equal(t, "A", strings.TrimSpace(row.Cells[0]))
	assert.Equal(t, "2", strings.TrimSpace(row.Cells[1]))
}

func TestWithSkipColumns(t *testing.T) {
	data := []tableFormatRow{{Name: "A", Rate: 0.1, Amount: 2}}
	events := collectEvents(t, "", data, WithSkipColumns("Rate"))
	begin, ok := events[0].(tableBegin)
	assert.True(t, ok)
	assert.Equal(t, []tableColumnInfo{
		{Header: "NAME", Kind: "string"},
		{Header: "AMOUNT", Kind: "int"},
	}, begin.Columns)
}

func TestWithIncludeAndSkipColumns(t *testing.T) {
	data := []tableFormatRow{{Name: "A", Rate: 0.1, Amount: 2}}
	events := collectEvents(t, "", data,
		WithIncludeColumns("Name", "Rate"),
		WithSkipColumns("Amount"),
	)
	begin, ok := events[0].(tableBegin)
	assert.True(t, ok)
	assert.Equal(t, []tableColumnInfo{
		{Header: "NAME", Kind: "string"},
		{Header: "RATE", Kind: "float64"},
	}, begin.Columns)
}

func TestWithIncludeSkipConflict(t *testing.T) {
	buf := &bytes.Buffer{}
	err := TableAuto(buf, []tableFormatRow{{Name: "A"}}, WithIncludeColumns("Name"), WithSkipColumns("Name"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be included and skipped")
}

func TestColumnOptionsUnknownColumn(t *testing.T) {
	buf := &bytes.Buffer{}
	type row struct {
		Name string
	}
	tests := []struct {
		name string
		opt  opt
	}{
		{
			name: "name format",
			opt:  WithColumnFormat("Missing", func(v string) string { return v }),
		},
		{
			name: "template",
			opt:  WithColumnTemplate("Missing", "{{.Name}}"),
		},
		{
			name: "include",
			opt:  WithIncludeColumns("Missing"),
		},
		{
			name: "skip",
			opt:  WithSkipColumns("Missing"),
		},
		{
			name: "green red scale",
			opt:  WithColumnGreenRedScale("Missing"),
		},
		{
			name: "red green scale",
			opt:  WithColumnRedGreenScale("Missing"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := TableAuto(buf, []row{{Name: "A"}}, tc.opt)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), `unknown column "Missing"`)
		})
	}
}

func TestWithFloat64AsPercent(t *testing.T) {
	data := []tableFormatRow{
		{Name: "A", Rate: 0.05873242},
		{Name: "B", Rate: -0.999999},
		{Name: "C", Rate: 0},
		{Name: "D", Rate: 0.049199},
		{Name: "E", Rate: 0.01},
	}
	events := collectEvents(t, "", data, WithFloat64AsPercent())
	assert.Equal(t, "5.87%", strings.TrimSpace(mustTableRow(t, events[1]).Cells[1]))
	assert.Equal(t, "-99%", strings.TrimSpace(mustTableRow(t, events[2]).Cells[1]))
	assert.Equal(t, "0%", strings.TrimSpace(mustTableRow(t, events[3]).Cells[1]))
	assert.Equal(t, "4.91%", strings.TrimSpace(mustTableRow(t, events[4]).Cells[1]))
	assert.Equal(t, "1%", strings.TrimSpace(mustTableRow(t, events[5]).Cells[1]))
}

func TestWithFloat64AsPercentPositiveColored(t *testing.T) {
	data := []tableFormatRow{
		{Name: "A", Rate: 0.05873242},
		{Name: "B", Rate: -0.999999},
		{Name: "C", Rate: 0},
	}
	events := collectEvents(t, "", data, WithFloat64AsPercentPositiveColored())

	pos, ok := events[1].(tableRow)
	assert.True(t, ok)
	neg, ok := events[2].(tableRow)
	assert.True(t, ok)
	zero, ok := events[3].(tableRow)
	assert.True(t, ok)

	assert.Contains(t, pos.Cells[1], green+"5.87%"+reset)
	assert.Contains(t, neg.Cells[1], red+"-99%"+reset)
	assert.Contains(t, zero.Cells[1], "0%")
	assert.NotContains(t, zero.Cells[1], green)
	assert.NotContains(t, zero.Cells[1], red)
}

func TestWithColumnGreenRedScaleNumeric(t *testing.T) {
	type row struct {
		Value float64
	}
	data := []row{
		{Value: 0},
		{Value: 100},
		{Value: 25},
		{Value: 50},
		{Value: 75},
		{Value: 0},
	}
	events := collectEvents(t, "", data, WithColumnGreenRedScale("Value"))
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[0])
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[0], brightRed, "100")
	assertScaledCellContains(t, mustTableRow(t, events[3]).Cells[0], yellow, "25")
	assertScaledCellContains(t, mustTableRow(t, events[4]).Cells[0], red, "50")
	assertScaledCellContains(t, mustTableRow(t, events[5]).Cells[0], red, "75")
	assertScaledCellContains(t, mustTableRow(t, events[6]).Cells[0], brightGreen, "0")
}

func TestWithColumnGreenRedScaleTime(t *testing.T) {
	type row struct {
		When time.Time `header:"When"`
	}
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	data := []row{
		{When: base},
		{When: base.Add(4 * time.Hour)},
		{When: base.Add(2 * time.Hour)},
		{When: base.Add(1 * time.Hour)},
		{When: base.Add(3 * time.Hour)},
	}
	events := collectEvents(t, "", data, WithColumnGreenRedScale("When"))
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[0])
	assert.Contains(t, mustTableRow(t, events[2]).Cells[0], brightRed)
	assert.Contains(t, mustTableRow(t, events[3]).Cells[0], yellow)
	assert.Contains(t, mustTableRow(t, events[4]).Cells[0], green)
	assert.Contains(t, mustTableRow(t, events[5]).Cells[0], red)
}

func TestWithColumnGreenRedScaleDuration(t *testing.T) {
	type row struct {
		Elapsed time.Duration
	}
	data := []row{
		{Elapsed: 0},
		{Elapsed: 4 * time.Second},
		{Elapsed: 2 * time.Second},
		{Elapsed: 1 * time.Second},
		{Elapsed: 3 * time.Second},
	}
	events := collectEvents(t, "", data, WithColumnGreenRedScale("Elapsed"))
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[0])
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[0], brightRed, "4s")
	assertScaledCellContains(t, mustTableRow(t, events[3]).Cells[0], yellow, "2s")
	assertScaledCellContains(t, mustTableRow(t, events[4]).Cells[0], green, "1s")
	assertScaledCellContains(t, mustTableRow(t, events[5]).Cells[0], red, "3s")
}

func TestWithColumnRedGreenScaleNumeric(t *testing.T) {
	type row struct {
		Value int
	}
	data := []row{
		{Value: 0},
		{Value: 100},
		{Value: 75},
		{Value: 0},
	}
	events := collectEvents(t, "", data, WithColumnRedGreenScale("Value"))
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[0])
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[0], brightGreen, "100")
	assertScaledCellContains(t, mustTableRow(t, events[3]).Cells[0], yellow, "75")
	assertScaledCellContains(t, mustTableRow(t, events[4]).Cells[0], brightRed, "0")
}

func TestWithColumnRedGreenScaleOutlierDoesNotSkewAllNegativesToGreen(t *testing.T) {
	type row struct {
		Value float64
	}
	data := []row{
		{Value: -0.20},
		{Value: 0.70},
		{Value: -20.60},
		{Value: -0.30},
	}
	events := collectEvents(t, "", data, WithColumnRedGreenScale("Value"))
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[0])
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[0], brightGreen, "0.70")
	assertScaledCellContains(t, mustTableRow(t, events[3]).Cells[0], brightRed, "-20.60")
	assertScaledCellContains(t, mustTableRow(t, events[4]).Cells[0], red, "-0.30")
}

func TestWithColumnGreenRedScaleFlatRangeHasNoColor(t *testing.T) {
	type row struct {
		Value int
	}
	data := []row{
		{Value: 7},
		{Value: 7},
		{Value: 7},
	}
	events := collectEvents(t, "", data, WithColumnGreenRedScale("Value"))
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[0])
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[2]).Cells[0])
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[3]).Cells[0])
}

func TestWithColumnGreenRedScaleUnsupportedType(t *testing.T) {
	type row struct {
		Name string
	}
	err := TableAuto(&bytes.Buffer{}, []row{{Name: "A"}}, WithColumnGreenRedScale("Name"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `column "Name": unsupported type string for green-red scale`)
}

func TestWithColumnGreenRedScaleIgnoredForExplicitTemplate(t *testing.T) {
	type row struct {
		Value float64
	}
	data := []row{{Value: 0}, {Value: 100}}
	events := collectEvents(t, "{{.Value}}", data, WithColumnGreenRedScale("Value"))
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[0])
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[2]).Cells[0])
}

func TestWithColumnRedGreenScaleIgnoredForExplicitTemplate(t *testing.T) {
	type row struct {
		Value float64
	}
	data := []row{{Value: 0}, {Value: 100}}
	events := collectEvents(t, "{{.Value}}", data, WithColumnRedGreenScale("Value"))
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[0])
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[2]).Cells[0])
}

func TestWithColumnGreenRedScaleTableIterStreaming(t *testing.T) {
	type row struct {
		Value int
	}
	data := []row{
		{Value: 10},
		{Value: 20},
		{Value: 15},
		{Value: 100},
	}
	events := collectIterEvents(t, "", data, WithColumnGreenRedScale("Value"))
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[0])
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[0], brightRed, "20")
	assertScaledCellContains(t, mustTableRow(t, events[3]).Cells[0], yellow, "15")
	assertScaledCellContains(t, mustTableRow(t, events[4]).Cells[0], brightRed, "100")
}

func TestWithColumnGreenRedScalePreservesTypeFormatting(t *testing.T) {
	data := []tableFormatRow{
		{Name: "A", Rate: 0.05},
		{Name: "B", Rate: 0.20},
	}
	events := collectEvents(t, "", data, WithFloat64AsPercent(), WithColumnGreenRedScale("Rate"))
	assert.Equal(t, "5%", strings.TrimSpace(mustTableRow(t, events[1]).Cells[1]))
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[1], brightRed, "20%")

	events = collectEvents(t, "", data, WithColumnGreenRedScale("Rate"), WithFloat64AsPercent())
	assert.Equal(t, "5%", strings.TrimSpace(mustTableRow(t, events[1]).Cells[1]))
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[1], brightRed, "20%")
}

func TestColumnOptionsIgnoredForExplicitTemplate(t *testing.T) {
	data := []tableFormatRow{{Name: "A", Rate: 0.05873242}}
	events := collectEvents(t, "{{.Name}}\t{{.Rate}}", data, WithFloat64AsPercent())
	assert.Equal(t, "0.05873242", strings.TrimSpace(mustTableRow(t, events[1]).Cells[1]))
}

func TestTableAutoRendersPointerReceiverStringer(t *testing.T) {
	type row struct {
		Status pointerStringerStruct
	}
	events := collectEvents(t, "", []row{{Status: pointerStringerStruct{Value: "ok"}}})
	assert.Equal(t, "stringer:ok", strings.TrimSpace(mustTableRow(t, events[1]).Cells[0]))
}

func TestTableAutoRendersNilPointerStringer(t *testing.T) {
	type row struct {
		Status *pointerStringerStruct
	}
	events := collectEvents(t, "", []row{{}})
	assert.Equal(t, "<nil>", strings.TrimSpace(mustTableRow(t, events[1]).Cells[0]))
}

func TestFactsBasicRender(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Name string
		Age  int
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Name: "Alice", Age: 30})
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 2, len(lines))
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "AGE")
	assert.Contains(t, lines[1], "Alice")
	assert.Contains(t, lines[1], "30")
}

func TestFactsWrapsOnNarrowTTY(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 14, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Alpha string
		Beta  string
		Gamma string
		Delta string
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Alpha: "a", Beta: "b", Gamma: "c", Delta: "d"})
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 4, len(lines))
	assert.Contains(t, buf.String(), "\n\n")
	assert.Contains(t, lines[0], "ALPHA")
	assert.Contains(t, lines[0], "BETA")
	assert.NotContains(t, lines[0], "GAMMA")
	assert.Contains(t, lines[1], "a")
	assert.Contains(t, lines[1], "b")
	assert.Contains(t, lines[2], "GAMMA")
	assert.Contains(t, lines[2], "DELTA")
	assert.Contains(t, lines[3], "c")
	assert.Contains(t, lines[3], "d")
}

func TestFactsWithMaxWidthCapsWideTTY(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Alpha string
		Beta  string
		Gamma string
		Delta string
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Alpha: "a", Beta: "b", Gamma: "c", Delta: "d"}, WithMaxWidth(14))
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 4, len(lines))
	assert.Contains(t, lines[2], "GAMMA")
}

func TestFactsWithMaxWidthDoesNotWidenNarrowTTY(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 14, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Alpha string
		Beta  string
		Gamma string
		Delta string
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Alpha: "a", Beta: "b", Gamma: "c", Delta: "d"}, WithMaxWidth(80))
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 4, len(lines))
}

func TestFactsHonorsColumnOptions(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	data := tableFormatRow{Name: "A", Rate: 0.1, Amount: 9}
	buf := &bytes.Buffer{}
	err := Facts(buf, data,
		WithIncludeColumns("Name", "Rate"),
		WithColumnFormat("Rate", func(v float64) string { return fmt.Sprintf("rate=%.1f", v) }),
	)
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 2, len(lines))
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "RATE")
	assert.NotContains(t, lines[0], "AMOUNT")
	assert.Contains(t, lines[1], "A")
	assert.Contains(t, lines[1], "rate=0.1")
}

func TestFactsAlignsColumnStartsAcrossGroups(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Alpha string
		Beta  string
		Gamma string
		Delta string
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{
		Alpha: "1234567",
		Beta:  "B",
		Gamma: "g",
		Delta: "d",
	}, WithMaxWidth(24))
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 4, len(lines))
	betaStart := strings.Index(lines[0], "BETA")
	deltaStart := strings.Index(lines[2], "DELTA")
	assert.True(t, betaStart >= 0)
	assert.Equal(t, betaStart, deltaStart)
}

func TestFactsAddsTrailingBlankLine(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Name string
		Age  int
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Name: "Alice", Age: 30})
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(buf.String(), "\n\n"))
}

func TestFactsHonorsAlignRightMetadata(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Left  string
		Right string `header:"RIGHT,align-right"`
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Left: "left", Right: "x"})
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 2, len(lines))
	rightHeaderStart := strings.Index(lines[0], "RIGHT")
	rightValueStart := strings.Index(lines[1], "x")
	assert.True(t, rightHeaderStart >= 0)
	assert.True(t, rightValueStart > rightHeaderStart)
}

func TestWithMaxWidthRejectsNonPositive(t *testing.T) {
	buf := &bytes.Buffer{}
	type row struct {
		Name string
	}
	err := Facts(buf, row{Name: "A"}, WithMaxWidth(0))
	assert.ErrorIs(t, err, ErrInvalidState)

	err = Facts(buf, row{Name: "A"}, WithMaxWidth(-1))
	assert.ErrorIs(t, err, ErrInvalidState)
}

func TestFormattersHandleNilPointerFields(t *testing.T) {
	type row struct {
		Name string
		Rate *float64
	}
	rate := 0.5
	data := []row{{Name: "A", Rate: &rate}, {Name: "B"}}
	events := collectEvents(t, "", data, WithFloat64AsPercent())
	assert.Equal(t, "50%", strings.TrimSpace(mustTableRow(t, events[1]).Cells[1]))
	assert.Equal(t, "", strings.TrimSpace(mustTableRow(t, events[2]).Cells[1]))

	events = collectEvents(t, "", data, WithColumnFormat("Rate", percentString))
	assert.Equal(t, "50%", strings.TrimSpace(mustTableRow(t, events[1]).Cells[1]))
	assert.Equal(t, "", strings.TrimSpace(mustTableRow(t, events[2]).Cells[1]))
}

func TestPercentStringTruncationBoundaries(t *testing.T) {
	assert.Equal(t, "57%", percentString(0.57))
	assert.Equal(t, "-57%", percentString(-0.57))
	assert.Equal(t, "29%", percentString(0.29))
	assert.Equal(t, "-29%", percentString(-0.29))
	assert.Equal(t, "56%", percentString(math.Nextafter(0.57, 0)))
	assert.Equal(t, "28%", percentString(math.Nextafter(0.29, 0)))
}

func TestFactsInterfaceInputUsesDynamicType(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	var data any = struct{ Name string }{"Alice"}
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, data))
	lines := factLines(buf.String())
	assert.Equal(t, 2, len(lines))
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[1], "Alice")

	var nilData any
	assert.Error(t, Facts(&bytes.Buffer{}, nilData))
	assert.Error(t, Facts(&bytes.Buffer{}, any(42)))
}

func TestTableInterfaceElementsRejected(t *testing.T) {
	buf := &bytes.Buffer{}
	assert.Error(t, Table[any](buf, "{{.}}", nil))
	assert.Error(t, TableAuto[any](buf, nil))
	assert.Error(t, TableIter[any](buf, "{{.}}", iterate[any](nil)))
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

func TestTableScaleColorFollowsPhysicalCell(t *testing.T) {
	type row struct {
		Text  string
		Value int
	}
	data := []row{{"warm", 1}, {"a\nb", 100}, {"single", 50}}
	events := collectEvents(t, "", data, WithColumnGreenRedScale("Value"), WithMultilineCells())
	assertNoGreenRedScaleColor(t, mustTableRow(t, events[1]).Cells[1])
	// multi-line record: value stays on the first physical row, continuation is plain
	first := mustTableRow(t, events[2]).Cells
	assert.Equal(t, "a", strings.TrimSpace(first[0]))
	assertScaledCellContains(t, first[1], brightRed, "100")
	cont := mustTableRow(t, events[3]).Cells
	assert.Equal(t, "b", strings.TrimSpace(cont[0]))
	assertNoGreenRedScaleColor(t, cont[0])
	assertNoGreenRedScaleColor(t, cont[1])
	last := mustTableRow(t, events[4]).Cells
	assertScaledCellContains(t, last[1], yellow, "50")
}

func TestTableMultilineCellsDisabledByDefault(t *testing.T) {
	type row struct {
		Text  string
		Value int
	}
	data := []row{{"a\nb", 1}, {"c\r\nd", 2}}
	events := collectEvents(t, "", data, WithColumnGreenRedScale("Value"))
	assert.Equal(t, 4, len(events))
	first := mustTableRow(t, events[1]).Cells
	assert.Equal(t, "a b", strings.TrimSpace(first[0]))
	second := mustTableRow(t, events[2]).Cells
	assert.Equal(t, "c d", strings.TrimSpace(second[0]))
	assertScaledCellContains(t, second[1], brightRed, "2")
}

func TestTableMultilineCellsAlignPhysicalRows(t *testing.T) {
	type row struct {
		Name string
		Note string
	}
	data := []row{{"alpha", "one\ntwo\nthree"}, {"beta", "four"}}
	buf := &bytes.Buffer{}
	assert.NoError(t, Table(buf, "{{.Name}}\t{{.Note}}", data, WithMultilineCells()))
	lines := strings.Split(strings.TrimRight(factPlain(buf.String()), "\n"), "\n")
	assert.Equal(t, 5, len(lines))
	assert.Equal(t, "alpha  one", strings.TrimRight(lines[1], " "))
	assert.Equal(t, "two", strings.TrimSpace(lines[2]))
	assert.Equal(t, strings.Index(lines[1], "one"), strings.Index(lines[2], "two"))
	assert.Equal(t, strings.Index(lines[1], "one"), strings.Index(lines[3], "three"))
	assert.Equal(t, strings.Index(lines[1], "one"), strings.Index(lines[4], "four"))
}

func TestTableMultilineCellsTruncateEachLine(t *testing.T) {
	type row struct {
		Text string
	}
	data := []row{{"a\nbb\nccc\ndddd\neeeeee\nff"}}
	// 3 visible characters, the truncation tailer and the cell padding
	o := []opt{WithMultilineCells(), WithMaxWidth(5)}

	events := collectEvents(t, "{{.Text}}", data, o...)
	assert.Equal(t, 8, len(events))
	var got []string
	for _, ev := range events[1 : len(events)-1] {
		got = append(got, strings.TrimSpace(mustTableRow(t, ev).Cells[0]))
	}
	assert.Equal(t, []string{"a", "bb", "ccc", "dd…", "ee…", "ff"}, got)

	buf := &bytes.Buffer{}
	assert.NoError(t, Table(buf, "{{.Text}}", data, o...))
	lines := strings.Split(strings.TrimRight(factPlain(buf.String()), "\n"), "\n")
	assert.Equal(t, 7, len(lines))
	assert.Equal(t, "TE…", strings.TrimSpace(lines[0]))
	for _, line := range lines {
		if text(line).width() > 5 {
			t.Fatalf("width %d > 5: %q", text(line).width(), line)
		}
	}
	assert.Equal(t, "dd…", strings.TrimSpace(lines[4]))
	assert.Equal(t, "ee…", strings.TrimSpace(lines[5]))
	assert.Equal(t, "ff", strings.TrimSpace(lines[6]))
}

func TestTableTrailingLineBreakEndsRow(t *testing.T) {
	type row struct {
		Name string
		ID   string
	}
	data := []row{{"foo", "1"}, {"bar", "2"}}
	for _, tmpl := range []string{"{{.Name}}\t{{.ID}}\n", "{{.Name}}\t{{.ID}}\r\n"} {
		for _, o := range [][]opt{nil, {WithMultilineCells()}} {
			events := collectEvents(t, tmpl, data, o...)
			assert.Equal(t, 4, len(events))
			assert.Equal(t, "1", strings.TrimSpace(mustTableRow(t, events[1]).Cells[1]))
			assert.Equal(t, "2", strings.TrimSpace(mustTableRow(t, events[2]).Cells[1]))

			buf := &bytes.Buffer{}
			assert.NoError(t, Table(buf, tmpl, data, o...))
			lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			assert.Equal(t, 3, len(lines))
		}
	}
}

func TestTableEmptyFirstBatchColumnKeepsMinimumWidth(t *testing.T) {
	type row struct {
		Name string
		Note string
	}
	data := make([]row, 12) // the first batch of 10 has no notes
	for i := range data {
		data[i].Name = fmt.Sprintf("n%02d", i)
	}
	data[11].Note = "late"
	suppress := opT(func(tbl *table) error {
		tbl.suppressHeaders = true
		return nil
	})

	events := collectEvents(t, "", data)
	last := mustTableRow(t, events[len(events)-2]).Cells
	assert.Equal(t, "…", strings.TrimSpace(last[1]))

	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, data, suppress))
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	assert.Equal(t, 12, len(lines))
	assert.Equal(t, "n11  …", strings.TrimRight(lines[11], " "))

	// the reserved minimum counts toward the max width budget
	for i := range data {
		data[i].Name = strings.Repeat("x", 30)
	}
	buf.Reset()
	assert.NoError(t, TableAuto(buf, data, suppress, WithMaxWidth(20)))
	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		if text(line).width() > 20 {
			t.Fatalf("width %d > 20: %q", text(line).width(), line)
		}
	}
}

func TestFactsTabIsMeasured(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		A string
		B string
	}
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, row{A: "a\tb", B: "x"}))
	assert.NotContains(t, buf.String(), "\t")
	lines := factLines(buf.String())
	assert.Equal(t, strings.Index(lines[0], "B"), strings.Index(lines[1], "x"))
}

func TestTableMaxWidthIsTotalRowBudget(t *testing.T) {
	type row struct {
		A, B, C string
	}
	long := strings.Repeat("x", 30)
	var data []row
	for range 25 { // spans the first and later batches
		data = append(data, row{long, long, long})
	}
	data[20] = row{strings.Repeat("y", 50), "s", long}

	for _, maxWidth := range []int{40, 20, 9} {
		buf := &bytes.Buffer{}
		assert.NoError(t, TableAuto(buf, data, WithMaxWidth(maxWidth)))
		lines := strings.Split(strings.TrimRight(factPlain(buf.String()), "\n"), "\n")
		assert.Equal(t, 26, len(lines))
		for _, line := range lines {
			if text(line).width() > maxWidth {
				t.Fatalf("width %d > %d: %q", text(line).width(), maxWidth, line)
			}
		}
	}

	// minimums that cannot fit are a floor, not an error
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, data[:2], WithMaxWidth(3)))
	assert.Equal(t, 3, len(strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")))
}

func factPlain(out string) string {
	return strings.NewReplacer("\x1b[1m", "", "\x1b[0m", "").Replace(out)
}

// themedTable turns the theme gate on with th installed and clears the color
// environment, then adds pairs such as FORCE_COLOR=3.
func themedTable(t *testing.T, th Theme, env ...string) {
	t.Helper()
	withEnv(t, env...)
	useTheme(t, th)
}

// stripSGR removes SGR sequences, keeping text and every other sequence.
func stripSGR(s string) string {
	var out text
	for seg := range text(s).segments() {
		if _, sgr := seg.sgrParams(); seg.isEscape && sgr {
			continue
		}
		out = append(out, seg.text...)
	}
	return out.String()
}

type themedRow struct {
	Name   string
	Amount int
	Note   string
}

var themedRows = []themedRow{{"alpha", 1, "first"}, {"beta", 22, ""}, {"gamma", 333, "third"}}

func TestThemedTableMatchesLegacyOnPlainWriter(t *testing.T) {
	render := func() []string {
		var outs []string
		for _, run := range []func(w io.Writer) error{
			func(w io.Writer) error { return TableAuto(w, dummyPets) },
			func(w io.Writer) error { return TableAuto(w, dummyPets, WithMaxWidth(30)) },
			func(w io.Writer) error { return Table(w, "{{.Name}}\t{{.Owner.Name}}\t{{.Age}}", dummyPets) },
			func(w io.Writer) error {
				return Table(w, "", []themedRow{{"a\nb", 1, "c\nd\ne"}}, WithMultilineCells())
			},
		} {
			buf := &bytes.Buffer{}
			assert.NoError(t, run(buf))
			outs = append(outs, buf.String())
		}
		return outs
	}
	withEnv(t)
	legacy := render()
	themedTable(t, NewTheme())
	themed := render()
	assert.Equal(t, legacy, themed)
	for _, out := range themed {
		assert.NotContains(t, out, "\x1b")
	}
}

func TestThemedTableRoles(t *testing.T) {
	th := NewTheme()
	th.Light = true
	themedTable(t, th, "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).roles
	assert.True(t, len(roles.foreground) > 0)
	out := buf.String()
	assert.Contains(t, out, bold+string(roles.foreground)+"NAME"+fgDefault+reset)
	assert.Contains(t, out, string(roles.foreground)+"alpha"+fgDefault)
	assert.Contains(t, out, string(roles.foreground)+"333"+fgDefault)
	// ordinary data is neither an outcome nor a focus
	for _, role := range []text{roles.accent, roles.success, roles.failure, roles.muted} {
		assert.NotContains(t, out, string(role))
	}
	assert.Equal(t, "NAME    AMOUNT NOTE   \nalpha        1 first  \n"+
		"beta        22        \ngamma      333 third  \n", stripSGR(out))
}

func TestThemedTableSlots(t *testing.T) {
	th := NewTheme()
	th.Templates.Table.Header = `{{ .Name }}:{{ .Kind }}`
	th.Templates.Table.Cell = `<{{ .Value }}>`
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows[:2]))
	assert.Equal(t, "Name:string   Amount:int Note:string  \n"+
		"<alpha>              <1> <first>      \n"+
		"<beta>              <22>              \n", buf.String())

	// an explicit row template keeps its cells; the header is still themed
	buf.Reset()
	assert.NoError(t, Table(buf, "{{.Name}}\t{{.Owner.Name}}\t{{.Owner.Age}}", dummyPets[1:2]))
	assert.Equal(t, "Name:string  Owner.Name:string  Owner.Age:int  \n"+
		"Fluffy       Alice              30             \n", buf.String())
}

func TestThemedTableHiddenHeader(t *testing.T) {
	th := NewTheme()
	th.Templates.Table.Header = Hidden
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows[:2]))
	assert.Equal(t, "alpha    1 first  \nbeta    22        \n", buf.String())
	buf.Reset()
	assert.NoError(t, Table(buf, "{{.Name}}", themedRows[:1]))
	assert.Equal(t, "alpha  \n", buf.String())
}

func TestThemedTableCellWrapsChosenFormat(t *testing.T) {
	th := NewTheme()
	th.Templates.Table.Cell = `[{{ .Value }}]`
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows[:1],
		WithColumnTemplate("Name", "T:{{.Name}}"),
		WithColumnFormat("Name", func(string) string { return "named" }),
		WithColumnFormat("Amount", func(int) string { return "named" }),
		WithColumnTypeFormat(func(int) string { return "typed" }),
		WithColumnTypeFormat(func(string) string { return "typed" }),
	))
	assert.Equal(t, "NAME         AMOUNT NOTE     \n[T:alpha]   [named] [typed]  \n", buf.String())
}

func TestThemedTableSignedPercent(t *testing.T) {
	data := []tableFormatRow{{"A", 0.05873242, 0}, {"B", -0.999999, 0}, {"C", 0, 0}}
	themedTable(t, NewTheme(), "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, Table(buf, "", data, WithFloat64AsPercentPositiveColored()))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).roles
	lines := strings.Split(buf.String(), "\n")
	assert.Contains(t, lines[1], string(roles.success)+"5.87%"+fgDefault)
	assert.Contains(t, lines[2], string(roles.failure)+"-99%"+fgDefault)
	assert.NotContains(t, lines[3], string(roles.success))
	assert.NotContains(t, lines[3], string(roles.failure))
	// the legacy codes are gone
	assert.NotContains(t, buf.String(), green)
	assert.NotContains(t, buf.String(), red)

	withEnv(t)
	buf.Reset()
	assert.NoError(t, Table(buf, "", data, WithFloat64AsPercentPositiveColored()))
	assert.Equal(t, "NAME    RATE  AMOUNT \nA      5.87%       0 \nB       -99%       0 \nC         0%       0 \n",
		buf.String())
}

func TestThemedTableScaleBuckets(t *testing.T) {
	type row struct {
		Value float64
	}
	data := []row{{0}, {100}, {25}, {50}, {75}, {0}}
	// rank buckets, as in TestWithColumnGreenRedScaleNumeric: the first
	// value has nothing to rank against
	want := []int{-1, 4, 2, 3, 3, 0}
	for _, force := range []string{"3", "2", "1"} {
		th := NewTheme()
		th.Light = true
		themedTable(t, th, "FORCE_COLOR="+force)
		for _, reversed := range []bool{false, true} {
			o := WithColumnGreenRedScale("Value")
			if reversed {
				o = WithColumnRedGreenScale("Value")
			}
			buf := &bytes.Buffer{}
			assert.NoError(t, TableAuto(buf, data, o))
			look := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil)
			scale := look.diverging()
			lines := strings.Split(buf.String(), "\n")
			for i, bucket := range want {
				line, value := lines[i+1], strings.TrimSpace(stripSGR(lines[i+1]))
				if bucket < 0 {
					// only the Cell slot's foreground
					assert.Equal(t, "   "+string(look.roles.foreground)+value+fgDefault+reset+" ", line)
					continue
				}
				if reversed {
					bucket = len(scale) - 1 - bucket
				}
				// buckets follow ranks even where levels encode alike
				assert.Contains(t, line, string(scale[bucket])+value)
			}
		}
	}
}

func TestThemedTableExtraCellsAreDropped(t *testing.T) {
	render := func() string {
		buf := &bytes.Buffer{}
		assert.NoError(t, TableAuto(buf, themedRows[:1], WithColumnTemplate("Note", "{{.Note}}\textra")))
		return buf.String()
	}
	withEnv(t)
	legacy := render()
	themedTable(t, NewTheme())
	assert.Equal(t, legacy, render())
}

func TestThemedTableScaleWithoutColor(t *testing.T) {
	type row struct {
		Value int
	}
	themedTable(t, NewTheme())
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, []row{{1}, {2}, {3}}, WithColumnGreenRedScale("Value")))
	assert.Equal(t, " VALUE \n     1 \n     2 \n     3 \n", buf.String())
}

func TestThemedTableMultilineCarriesStyles(t *testing.T) {
	type row struct {
		Note string
		Name string
	}
	themedTable(t, NewTheme(), "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, Table(buf, "{{ green .Note }}\t{{ .Name }}", []row{{"one\ntwo\n\nfour", "x"}},
		WithMultilineCells()))
	// every line closes its style before the next column; continuation
	// lines reopen it, except the empty one
	assert.Equal(t, bold+"NOTE"+reset+"  "+bold+"NAME"+reset+"  \n"+
		green+"one"+reset+"   x     \n"+
		green+"two"+reset+"         \n"+
		"            \n"+
		green+"four"+reset+"        \n", buf.String())
}

func TestThemedTableStyle(t *testing.T) {
	th := NewTheme()
	th.Table = TableStyle{ColumnGap: 4, MinColumnWidth: 3}
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows))
	assert.Equal(t, "NAME        AMOUNT NOTE     \n"+
		"alpha            1 first    \n"+
		"beta            22          \n"+
		"gamma          333 third    \n", buf.String())

	// the minimum column width is a floor under the max width
	buf.Reset()
	assert.NoError(t, TableAuto(buf, themedRows[:1], WithMaxWidth(10)))
	assert.Equal(t, "NA…       AM… NO…    \nal…         1 fi…    \n", buf.String())
}

func TestThemedTableWidthLockAfterFirstBatch(t *testing.T) {
	data := make([]themedRow, 12)
	for i := range data {
		data[i] = themedRow{Name: fmt.Sprintf("n%02d", i), Amount: i}
	}
	data[11] = themedRow{Name: "a much longer name", Amount: 123456789, Note: "much later"}
	th := NewTheme()
	th.Light = true
	themedTable(t, th)
	plain := &bytes.Buffer{}
	assert.NoError(t, TableAuto(plain, data))
	lines := strings.Split(plain.String(), "\n")
	assert.Equal(t, "a m…   12345… muc…  ", lines[12])

	// decoration does not count toward widths
	withEnv(t, "FORCE_COLOR=3")
	colored := &bytes.Buffer{}
	assert.NoError(t, TableAuto(colored, data))
	assert.True(t, colored.String() != plain.String())
	assert.Equal(t, plain.String(), stripSGR(colored.String()))
}

func TestThemedTableCellKeepsHyperlinksDropsControls(t *testing.T) {
	type row struct {
		Link string
	}
	themedTable(t, NewTheme())
	link := "\x1b]8;;https://example.com\x1b\\site\x1b]8;;\x1b\\"
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, []row{{"a\x07b\x1b[2Jc"}, {link}}))
	assert.Equal(t, "LINK  \nabc   \n"+link+"  \n", buf.String())
}

func TestThemedTableRowTemplateHelpersBindToWriter(t *testing.T) {
	tmpl := "{{ green .Name }}\t{{ success .Name }}\t{{ fg \"#FF0000\" .Name }}"
	themedTable(t, NewTheme())
	buf := &bytes.Buffer{}
	assert.NoError(t, Table(buf, tmpl, themedRows[:1]))
	assert.NotContains(t, buf.String(), "\x1b")

	withEnv(t, "FORCE_COLOR=3")
	buf.Reset()
	assert.NoError(t, Table(buf, tmpl, themedRows[:1]))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).roles
	assert.Contains(t, buf.String(), green+"alpha")
	assert.Contains(t, buf.String(), string(roles.success)+"alpha")
	assert.Contains(t, buf.String(), "\x1b[38;2;255;0;0malpha")
}

func TestThemedTableErrors(t *testing.T) {
	boom := template.FuncMap{
		"boom": func(string) (string, error) { return "", errors.New("boom") },
		"bell": func() string { return "\a" },
	}
	for _, tc := range []struct {
		edit func(*TableTemplates)
		want string
	}{
		{func(s *TableTemplates) { s.Header = `{{ boom .Header }}` }, "table: headers: theme.Templates.Table.Header: "},
		{
			func(s *TableTemplates) { s.Cell = `{{ if eq .Name "Amount" }}{{ boom .Value }}{{ end }}{{ .Value }}` },
			`row 0: append: render: column "Amount": theme.Templates.Table.Cell: `,
		},
		{
			func(s *TableTemplates) { s.Cell = `{{ .Value }}{{ bell }}` },
			`row 0: append: render: column "Name": theme.Templates.Table.Cell: "\a": want text, SGR and hyperlinks only`,
		},
	} {
		th := NewTheme()
		th.Funcs = boom
		tc.edit(&th.Templates.Table)
		themedTable(t, th)
		buf := &bytes.Buffer{}
		err := TableAuto(buf, themedRows)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
		assert.Equal(t, "", buf.String())
	}

	themedTable(t, NewTheme())
	err := TableAuto(errWriterTable{}, themedRows)
	assert.ErrorIs(t, err, io.EOF)
}

func TestTableStaysLegacyWhenOff(t *testing.T) {
	keepTheme(t)
	prev := themeRendering.Swap(false)
	t.Cleanup(func() { themeRendering.Store(prev) })
	th := NewTheme()
	th.Templates.Table.Cell = `<{{ .Value }}>`
	assert.NoError(t, defaultIO.installTheme(th))
	withEnv(t)
	buf := &bytes.Buffer{}
	assert.NoError(t, Table(buf, "", []tableFormatRow{{"A", 0.5, 1}}, WithFloat64AsPercentPositiveColored()))
	assert.Equal(t, "NAME   RATE  AMOUNT \nA       "+green+"50%"+reset+"       1 \n", buf.String())
}

// streamedRows are two batches and a bit: the twelfth row is wider than the
// first batch allows.
func streamedRows() []themedRow {
	data := make([]themedRow, 12)
	for i := range data {
		data[i] = themedRow{Name: fmt.Sprintf("n%02d", i), Amount: i}
	}
	data[11] = themedRow{Name: "a much longer name", Amount: 123456789, Note: "much later"}
	return data
}

// lines splits output into its rows, without the final line break.
func lines(out string) []string {
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

func TestThemedTableIterMatchesTable(t *testing.T) {
	data := streamedRows()
	for _, env := range [][]string{nil, {"FORCE_COLOR=3"}} {
		th := NewTheme()
		th.Light = true
		themedTable(t, th, env...)
		iterated, whole := &bytes.Buffer{}, &bytes.Buffer{}
		assert.NoError(t, TableIter(iterated, "", iterate(data)))
		assert.NoError(t, TableAuto(whole, data))
		assert.Equal(t, whole.String(), iterated.String())
		// widths lock after the first batch, decoration not counted
		assert.Equal(t, "a m…   12345… muc…  ", stripSGR(lines(iterated.String())[12]))
	}
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), &bytes.Buffer{}, nil).roles
	buf := &bytes.Buffer{}
	assert.NoError(t, TableIter(buf, "", iterate(data[:1])))
	fg := func(s string) string { return string(roles.foreground) + s + fgDefault }
	assert.Equal(t, bold+fg("NAME")+reset+"   "+bold+fg("AMOUNT")+reset+" "+bold+fg("NOTE")+reset+"  \n"+
		fg("n00")+reset+"         "+fg("0")+reset+"       \n", buf.String())

	// a plain writer gets exactly the legacy bytes
	withEnv(t)
	themed := &bytes.Buffer{}
	assert.NoError(t, TableIter(themed, "", iterate(data)))
	themeRendering.Store(false)
	legacy := &bytes.Buffer{}
	assert.NoError(t, TableIter(legacy, "", iterate(data)))
	assert.Equal(t, legacy.String(), themed.String())
}

func TestThemedTableIterKeepsOneSnapshot(t *testing.T) {
	data := streamedRows()
	th := NewTheme()
	th.Light = true
	themedTable(t, th, "FORCE_COLOR=3")
	want := &bytes.Buffer{}
	assert.NoError(t, TableIter(want, "", iterate(data)))

	other := NewTheme()
	other.Templates.Table.Header = Hidden
	other.Templates.Table.Cell = `<{{ .Value }}>`
	other.Table = TableStyle{ColumnGap: 5}
	buf := &bytes.Buffer{}
	var firstBatch string
	changing := func(yield func(themedRow, error) bool) {
		for i, v := range data {
			switch i {
			case 0:
				// the theme is captured before the first row
				assert.NoError(t, defaultIO.installTheme(other))
			case 10:
				// the first batch is out, and later batches keep the
				// writer's capabilities
				firstBatch = buf.String()
				withEnv(t)
			}
			if !yield(v, nil) {
				return
			}
		}
	}
	assert.NoError(t, TableIter(buf, "", changing))
	assert.Equal(t, want.String(), buf.String())
	assert.Equal(t, strings.Join(lines(want.String())[:11], "\n")+"\n", firstBatch)

	// the next table takes the new theme and environment
	buf.Reset()
	assert.NoError(t, TableIter(buf, "", iterate(data[:1])))
	assert.Equal(t, "<n00>         <0>       \n", buf.String())
}

func TestThemedTableIterScaleRanksSeenSoFar(t *testing.T) {
	type row struct {
		Value int
	}
	var data []row
	for i := 1; i <= 10; i++ {
		data = append(data, row{i * 10})
	}
	data = append(data, row{0}, row{55}, row{100})
	// every value of the first batch is the largest so far; later ones rank
	// against everything seen, and earlier rows keep their colors
	want := []int{-1, 4, 4, 4, 4, 4, 4, 4, 4, 4, 0, 2, 4}
	th := NewTheme()
	th.Light = true
	themedTable(t, th, "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	var firstBatch string
	watched := func(yield func(row, error) bool) {
		for i, v := range data {
			if i == 10 {
				firstBatch = buf.String()
			}
			if !yield(v, nil) {
				return
			}
		}
	}
	assert.NoError(t, TableIter(buf, "", watched, WithColumnGreenRedScale("Value")))
	look := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil)
	scale := look.diverging()
	out := lines(buf.String())
	assert.Equal(t, len(data)+1, len(out))
	assert.True(t, strings.HasPrefix(buf.String(), firstBatch))
	assert.Equal(t, strings.Join(out[:11], "\n")+"\n", firstBatch)
	for i, bucket := range want {
		line, value := out[i+1], strings.TrimSpace(stripSGR(out[i+1]))
		if bucket < 0 {
			assert.Equal(t, "    "+string(look.roles.foreground)+value+fgDefault+reset+" ", line)
			continue
		}
		assert.Contains(t, line, string(scale[bucket])+value)
	}
}

func TestThemedTableIterErrors(t *testing.T) {
	data := streamedRows()
	th := NewTheme()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Table.Cell = `{{ if eq .Value "n10" }}{{ boom }}{{ end }}{{ .Value }}`
	themedTable(t, th)
	want := &bytes.Buffer{}
	assert.NoError(t, TableIter(want, "", iterate(data[:10])))
	firstBatch := want.String()

	// a producer failure keeps the rows already written, and drops the rest
	failing := func(yield func(themedRow, error) bool) {
		for _, v := range data[:10] {
			if !yield(v, nil) {
				return
			}
		}
		yield(themedRow{}, errors.New("gone"))
	}
	buf := &bytes.Buffer{}
	err := TableIter(buf, "", failing)
	assert.Error(t, err)
	assert.Equal(t, "row 10: fetch: gone", err.Error())
	assert.Equal(t, firstBatch, buf.String())

	// so does a Cell slot failing in a later batch
	buf.Reset()
	err = TableIter(buf, "", iterate(data))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `row 10: append: render: column "Name": theme.Templates.Table.Cell: `)
	assert.Contains(t, err.Error(), "boom")
	assert.Equal(t, firstBatch, buf.String())

	err = TableIter(errWriterTable{}, "", iterate(data))
	assert.ErrorIs(t, err, io.EOF)
}

// ttyWidth makes every writer report width columns.
func ttyWidth(t *testing.T, width int) {
	t.Helper()
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return width, 40, nil }
	t.Cleanup(func() { termGetSize = orig })
}

type factsRow struct {
	Left  string
	Count int
	Right string `header:"RIGHT,align-right"`
	Owner Person
}

var factsData = factsRow{Left: "left", Count: 7, Right: "x", Owner: Person{"Alice", 30}}

func TestThemedFactsMatchesLegacyOnPlainWriter(t *testing.T) {
	type four struct{ Alpha, Beta, Gamma, Delta string }
	render := func() []string {
		var outs []string
		for _, tc := range []struct {
			width int
			run   func(w io.Writer) error
		}{
			{120, func(w io.Writer) error { return Facts(w, factsData) }},
			{14, func(w io.Writer) error { return Facts(w, four{"a", "b", "c", "d"}) }},
			{120, func(w io.Writer) error { return Facts(w, four{"1234567", "B", "g", "d"}, WithMaxWidth(24)) }},
			{120, func(w io.Writer) error { return Facts(w, factsData, WithMaxWidth(12)) }},
			{120, func(w io.Writer) error {
				return Facts(w, tableFormatRow{"A", 0.1, 9}, WithIncludeColumns("Name", "Rate"),
					WithColumnFormat("Rate", func(v float64) string { return fmt.Sprintf("rate=%.1f", v) }))
			}},
			{120, func(w io.Writer) error { return Facts[any](w, dummyPets[0]) }},
		} {
			ttyWidth(t, tc.width)
			buf := &bytes.Buffer{}
			assert.NoError(t, tc.run(buf))
			outs = append(outs, buf.String())
		}
		return outs
	}
	withEnv(t)
	legacy := render()
	themedTable(t, NewTheme())
	themed := render()
	assert.Equal(t, legacy, themed)
	for _, out := range themed {
		assert.NotContains(t, out, "\x1b")
	}
}

func TestThemedFactsRoles(t *testing.T) {
	ttyWidth(t, 120)
	th := NewTheme()
	th.Light = true
	themedTable(t, th, "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, factsData))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).roles
	assert.True(t, len(roles.foreground) > 0)
	out := buf.String()
	assert.Contains(t, out, bold+string(roles.foreground)+"OWNER.NAME"+fgDefault+reset)
	assert.Contains(t, out, string(roles.foreground)+"Alice"+fgDefault)
	for _, role := range []text{roles.accent, roles.success, roles.failure, roles.muted} {
		assert.NotContains(t, out, string(role))
	}
	plain := &bytes.Buffer{}
	withEnv(t)
	assert.NoError(t, Facts(plain, factsData))
	assert.Equal(t, plain.String(), stripSGR(out))
}

func TestThemedFactsSlots(t *testing.T) {
	ttyWidth(t, 120)
	th := NewTheme()
	th.Templates.Table.Header = `{{ .Name }}:{{ .Kind }}`
	th.Templates.Table.Cell = `[{{ .Value }}]`
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, factsRow{Left: "l", Count: 3, Owner: Person{Name: "Bo"}},
		WithColumnTemplate("Left", "T:{{.Left}}"),
		WithColumnFormat("Count", func(int) string { return "named" }),
		WithColumnTypeFormat(func(int) string { return "typed" })))
	// the value wraps the chosen format; an empty value stays empty
	assert.Equal(t, "Left:string   Count:int  Right:string Owner.Name:string  Owner.Age:int  \n"+
		"[T:l]           [named]               [Bo]               [typed]        \n\n", buf.String())
}

func TestThemedFactsHiddenKey(t *testing.T) {
	ttyWidth(t, 120)
	th := NewTheme()
	th.Templates.Table.Header = Hidden
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, factsData))
	assert.Equal(t, "left   7  x Alice  30  \n\n", buf.String())
}

func TestThemedFactsStyle(t *testing.T) {
	ttyWidth(t, 120)
	th := NewTheme()
	th.Table = TableStyle{ColumnGap: 4, MinColumnWidth: 3}
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, tableFormatRow{"A", 0.5, 1}))
	assert.Equal(t, "NAME       RATE    AMOUNT \nA          0.50         1 \n\n", buf.String())

	// the minimum column width is a floor under the max width, as in tables
	buf.Reset()
	assert.NoError(t, Facts(buf, tableFormatRow{"Alpha", 0.5, 1}, WithMaxWidth(4)))
	assert.Equal(t, "NA…    \nAl…    \n\n   RA… \n   0.… \n\n   AM… \n     1 \n\n", buf.String())
}

func TestThemedFactsSignedPercent(t *testing.T) {
	ttyWidth(t, 120)
	themedTable(t, NewTheme(), "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, tableFormatRow{"A", -0.5, 1}, WithFloat64AsPercentPositiveColored()))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).roles
	assert.Contains(t, buf.String(), string(roles.failure)+"-50%"+fgDefault)
	assert.NotContains(t, buf.String(), red)
}

func TestThemedFactsValueLine(t *testing.T) {
	ttyWidth(t, 120)
	type row struct {
		Note string
		Link string
	}
	th := NewTheme()
	th.Templates.Table.Cell = `<{{ .Value }}>`
	themedTable(t, th)
	link := "\x1b]8;;https://example.com\x1b\\site\x1b]8;;\x1b\\"
	buf := &bytes.Buffer{}
	// the line the legacy presentation shows, without controls
	assert.NoError(t, Facts(buf, row{Note: "a\x07b\x1b[2Jc\nhidden", Link: link}))
	assert.Equal(t, "NOTE   LINK    \n<abc>  <"+link+">  \n\n", buf.String())
}

func TestThemedFactsErrors(t *testing.T) {
	ttyWidth(t, 120)
	boom := template.FuncMap{
		"boom": func(string) (string, error) { return "", errors.New("boom") },
		"bell": func() string { return "\a" },
	}
	for _, tc := range []struct {
		edit func(*TableTemplates)
		want string
	}{
		{
			func(s *TableTemplates) {
				s.Header = `{{ if eq .Name "Count" }}{{ boom .Header }}{{ end }}{{ .Header }}`
			},
			`facts: column "Count": theme.Templates.Table.Header: `,
		},
		{
			func(s *TableTemplates) { s.Header = `{{ .Header }}{{ bell }}` },
			`facts: column "Left": theme.Templates.Table.Header: "\a": want text and SGR only`,
		},
		{func(s *TableTemplates) { s.Cell = `{{ boom .Value }}` }, `facts: column "Left": theme.Templates.Table.Cell: `},
		{
			func(s *TableTemplates) { s.Cell = `{{ .Value }}{{ bell }}` },
			`facts: column "Left": theme.Templates.Table.Cell: "\a": want text, SGR and hyperlinks only`,
		},
	} {
		th := NewTheme()
		th.Funcs = boom
		tc.edit(&th.Templates.Table)
		themedTable(t, th)
		buf := &bytes.Buffer{}
		err := Facts(buf, factsData)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
		assert.Equal(t, "", buf.String())
	}

	themedTable(t, NewTheme())
	assert.ErrorIs(t, Facts(errWriterTable{}, factsData), io.EOF)
}

func TestFactsStaysLegacyWhenOff(t *testing.T) {
	ttyWidth(t, 120)
	keepTheme(t)
	prev := themeRendering.Swap(false)
	t.Cleanup(func() { themeRendering.Store(prev) })
	th := NewTheme()
	th.Templates.Table.Cell = `<{{ .Value }}>`
	th.Table = TableStyle{ColumnGap: 5}
	assert.NoError(t, defaultIO.installTheme(th))
	withEnv(t)
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, tableFormatRow{"A", 0.5, 1}, WithFloat64AsPercentPositiveColored()))
	assert.Equal(t, "NAME   RATE  AMOUNT \nA       "+green+"50%"+reset+"       1 \n\n", buf.String())
}
