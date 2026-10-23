// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package table

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
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
		"{{ bold .Name | accent }}\t{{ .Age }}\t{{ .Type }}\t{{ .Owner.Name }}\t{{ .Owner.Age }}",
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
	err := Table(buf, "{{.Name}}\t{{.Age}}", data, option.OpT(func(tbl *table) error {
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
	err := TableIter(terst.FailingWriter{Err: io.EOF}, "{{.Name}}", iter, option.OpT(func(t *table) error {
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
	err := TableAuto(buf, []int{1, 2})
	assert.Error(t, err)
}

func TestTableInvalidTemplate(t *testing.T) {
	buf := &bytes.Buffer{}
	err := Table(buf, "{{", []Person{{Name: "A"}})
	assert.Error(t, err)
}

func TestTableAppendWriteError(t *testing.T) {
	tbl, err := newTable[Person](terst.FailingWriter{Err: io.EOF}, "{{.Name}}")
	assert.NoError(t, err)
	tbl.batchSize = 1
	err = tbl.Append(Person{Name: "A"})
	assert.Error(t, err)
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
	err := TableAuto(&bytes.Buffer{}, data, option.WithHide())
	assert.ErrorIs(t, err, errs.ErrWrongWidget)
	assert.Contains(t, err.Error(), "need option.Prompt, got *table.table")

	err = TableAuto(&bytes.Buffer{}, data, option.WithDefault("a"))
	assert.ErrorIs(t, err, errs.ErrWrongWidget)
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
func collectEvents[T any](t *testing.T, rowTmpl string, data []T, o ...option.Opt) []tableEvent {
	t.Helper()
	var events []tableEvent
	o = append(o, option.OpT(func(tbl *table) error {
		tbl.eventSink = func(ev tableEvent) {
			events = append(events, ev)
		}
		return nil
	}))
	err := Table(bytes.NewBuffer(nil), rowTmpl, data, o...)
	assert.NoError(t, err)
	return events
}

func collectIterEvents[T any](t *testing.T, rowTmpl string, data []T, o ...option.Opt) []tableEvent {
	t.Helper()
	var events []tableEvent
	o = append(o, option.OpT(func(tbl *table) error {
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

// scaleSGR forces truecolor into buffers under the light preset, where the
// levels of a scale are distinct, and returns them from the success end. The
// middle level is the foreground, which the Cell slot also uses.
func scaleSGR(t *testing.T) []string {
	t.Helper()
	th := theme.New()
	th.Light = true
	themedTable(t, th, "FORCE_COLOR=3")
	var levels []string
	for _, c := range term.Default.BindTheme(t.Context(), term.Default.CurrentTheme(), &bytes.Buffer{}, nil).Diverging() {
		assert.True(t, !slices.Contains(levels, string(c)))
		levels = append(levels, string(c))
	}
	return levels
}

// assertNoGreenRedScaleColor checks that cell has none of the scale levels
// but the middle one, which is also the foreground of every cell.
func assertNoGreenRedScaleColor(t *testing.T, levels []string, cell string) {
	t.Helper()
	for i, c := range levels {
		if i != len(levels)/2 {
			assert.NotContains(t, cell, c)
		}
	}
}

func assertScaledCellContains(t *testing.T, cell, color, text string) {
	t.Helper()
	assert.Contains(t, cell, color+text)
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

func TestTableInterfaceElementsRejected(t *testing.T) {
	buf := &bytes.Buffer{}
	assert.Error(t, Table[any](buf, "{{.}}", nil))
	assert.Error(t, TableAuto[any](buf, nil))
	assert.Error(t, TableIter[any](buf, "{{.}}", iterate[any](nil)))
}

func TestTableScaleColorFollowsPhysicalCell(t *testing.T) {
	lv := scaleSGR(t)
	type row struct {
		Text  string
		Value int
	}
	data := []row{{"warm", 1}, {"a\nb", 100}, {"single", 50}}
	events := collectEvents(t, "", data, WithColumnGreenRedScale("Value"), WithMultilineCells())
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[1]).Cells[1])
	// multi-line record: value stays on the first physical row, continuation is plain
	first := mustTableRow(t, events[2]).Cells
	assert.Equal(t, "a", strings.TrimSpace(terst.StripSGR(first[0])))
	assertScaledCellContains(t, first[1], lv[4], "100")
	cont := mustTableRow(t, events[3]).Cells
	assert.Equal(t, "b", strings.TrimSpace(terst.StripSGR(cont[0])))
	assertNoGreenRedScaleColor(t, lv, cont[0])
	assertNoGreenRedScaleColor(t, lv, cont[1])
	last := mustTableRow(t, events[4]).Cells
	assertScaledCellContains(t, last[1], lv[2], "50")
}

func TestTableMultilineCellsDisabledByDefault(t *testing.T) {
	lv := scaleSGR(t)
	type row struct {
		Text  string
		Value int
	}
	data := []row{{"a\nb", 1}, {"c\r\nd", 2}}
	events := collectEvents(t, "", data, WithColumnGreenRedScale("Value"))
	assert.Equal(t, 4, len(events))
	first := mustTableRow(t, events[1]).Cells
	assert.Equal(t, "a b", strings.TrimSpace(terst.StripSGR(first[0])))
	second := mustTableRow(t, events[2]).Cells
	assert.Equal(t, "c d", strings.TrimSpace(terst.StripSGR(second[0])))
	assertScaledCellContains(t, second[1], lv[4], "2")
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
	o := []option.Opt{WithMultilineCells(), WithMaxWidth(5)}

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
		if ansi.Text(line).Width() > 5 {
			t.Fatalf("width %d > 5: %q", ansi.Text(line).Width(), line)
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
		for _, o := range [][]option.Opt{nil, {WithMultilineCells()}} {
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
	suppress := option.OpT(func(tbl *table) error {
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
		if ansi.Text(line).Width() > 20 {
			t.Fatalf("width %d > 20: %q", ansi.Text(line).Width(), line)
		}
	}
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
			if ansi.Text(line).Width() > maxWidth {
				t.Fatalf("width %d > %d: %q", ansi.Text(line).Width(), maxWidth, line)
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

type themedRow struct {
	Name   string
	Amount int
	Note   string
}

var themedRows = []themedRow{{"alpha", 1, "first"}, {"beta", 22, ""}, {"gamma", 333, "third"}}

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

// ttyWidth makes every writer report width columns.
func ttyWidth(t *testing.T, width int) {
	t.Helper()
	orig := term.GetSize
	term.GetSize = func(int) (int, int, error) { return width, 40, nil }
	t.Cleanup(func() { term.GetSize = orig })
}

type factsRow struct {
	Left  string
	Count int
	Right string `header:"RIGHT,align-right"`
	Owner Person
}

var factsData = factsRow{Left: "left", Count: 7, Right: "x", Owner: Person{"Alice", 30}}

func TestTableRemovedColorNamesNameTheirTemplate(t *testing.T) {
	type row struct{ Name string }
	themedTable(t, theme.New())
	err := Table(&bytes.Buffer{}, `{{ red .Name }}`, []row{{"a"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `row template:1: function "red" not defined`)

	err = TableAuto(&bytes.Buffer{}, []row{{"a"}}, WithColumnTemplate("Name", `{{ red .Name }}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `column "Name" template:1: function "red" not defined`)
}
