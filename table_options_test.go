// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"fmt"
	"github.com/nfx/go-tui/internal/option"
	"strings"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

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
		opt  option.Opt
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

func TestWithColumnGreenRedScaleTime(t *testing.T) {
	lv := scaleSGR(t)
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
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[1]).Cells[0])
	assert.Contains(t, mustTableRow(t, events[2]).Cells[0], lv[4])
	assert.Contains(t, mustTableRow(t, events[3]).Cells[0], lv[2])
	assert.Contains(t, mustTableRow(t, events[4]).Cells[0], lv[1])
	assert.Contains(t, mustTableRow(t, events[5]).Cells[0], lv[3])
}

func TestWithColumnGreenRedScaleDuration(t *testing.T) {
	lv := scaleSGR(t)
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
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[1]).Cells[0])
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[0], lv[4], "4s")
	assertScaledCellContains(t, mustTableRow(t, events[3]).Cells[0], lv[2], "2s")
	assertScaledCellContains(t, mustTableRow(t, events[4]).Cells[0], lv[1], "1s")
	assertScaledCellContains(t, mustTableRow(t, events[5]).Cells[0], lv[3], "3s")
}

func TestWithColumnRedGreenScaleNumeric(t *testing.T) {
	lv := scaleSGR(t)
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
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[1]).Cells[0])
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[0], lv[0], "100")
	assertScaledCellContains(t, mustTableRow(t, events[3]).Cells[0], lv[2], "75")
	assertScaledCellContains(t, mustTableRow(t, events[4]).Cells[0], lv[4], "0")
}

func TestWithColumnRedGreenScaleOutlierDoesNotSkewAllNegativesToGreen(t *testing.T) {
	lv := scaleSGR(t)
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
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[1]).Cells[0])
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[0], lv[0], "0.70")
	assertScaledCellContains(t, mustTableRow(t, events[3]).Cells[0], lv[4], "-20.60")
	assertScaledCellContains(t, mustTableRow(t, events[4]).Cells[0], lv[3], "-0.30")
}

func TestWithColumnGreenRedScaleFlatRangeHasNoColor(t *testing.T) {
	lv := scaleSGR(t)
	type row struct {
		Value int
	}
	data := []row{
		{Value: 7},
		{Value: 7},
		{Value: 7},
	}
	events := collectEvents(t, "", data, WithColumnGreenRedScale("Value"))
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[1]).Cells[0])
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[2]).Cells[0])
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[3]).Cells[0])
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
	lv := scaleSGR(t)
	type row struct {
		Value float64
	}
	data := []row{{Value: 0}, {Value: 100}}
	events := collectEvents(t, "{{.Value}}", data, WithColumnGreenRedScale("Value"))
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[1]).Cells[0])
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[2]).Cells[0])
}

func TestWithColumnRedGreenScaleIgnoredForExplicitTemplate(t *testing.T) {
	lv := scaleSGR(t)
	type row struct {
		Value float64
	}
	data := []row{{Value: 0}, {Value: 100}}
	events := collectEvents(t, "{{.Value}}", data, WithColumnRedGreenScale("Value"))
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[1]).Cells[0])
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[2]).Cells[0])
}

func TestWithColumnGreenRedScaleTableIterStreaming(t *testing.T) {
	lv := scaleSGR(t)
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
	assertNoGreenRedScaleColor(t, lv, mustTableRow(t, events[1]).Cells[0])
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[0], lv[4], "20")
	assertScaledCellContains(t, mustTableRow(t, events[3]).Cells[0], lv[2], "15")
	assertScaledCellContains(t, mustTableRow(t, events[4]).Cells[0], lv[4], "100")
}

func TestWithColumnGreenRedScalePreservesTypeFormatting(t *testing.T) {
	lv := scaleSGR(t)
	data := []tableFormatRow{
		{Name: "A", Rate: 0.05},
		{Name: "B", Rate: 0.20},
	}
	events := collectEvents(t, "", data, WithFloat64AsPercent(), WithColumnGreenRedScale("Rate"))
	assert.Equal(t, "5%", strings.TrimSpace(terst.StripSGR(mustTableRow(t, events[1]).Cells[1])))
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[1], lv[4], "20%")

	events = collectEvents(t, "", data, WithColumnGreenRedScale("Rate"), WithFloat64AsPercent())
	assert.Equal(t, "5%", strings.TrimSpace(terst.StripSGR(mustTableRow(t, events[1]).Cells[1])))
	assertScaledCellContains(t, mustTableRow(t, events[2]).Cells[1], lv[4], "20%")
}

func TestColumnOptionsIgnoredForExplicitTemplate(t *testing.T) {
	data := []tableFormatRow{{Name: "A", Rate: 0.05873242}}
	events := collectEvents(t, "{{.Name}}\t{{.Rate}}", data, WithFloat64AsPercent())
	assert.Equal(t, "0.05873242", strings.TrimSpace(mustTableRow(t, events[1]).Cells[1]))
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
