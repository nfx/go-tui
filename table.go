// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"io"
	"iter"

	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/table"
)

// TableIter renders rows as the iterator yields them, writing every ten.
// Column widths lock after the first batch, scales rank the values seen so
// far, and written rows are never repainted. The theme and the writer's
// capabilities are captured before the first row, and hold for every batch.
func TableIter[T any](w io.Writer, rowTmpl string, iterator iter.Seq2[T, error], o ...option.Opt) error {
	return table.TableIter[T](w, rowTmpl, iterator, o...)
}

func Table[T any](w io.Writer, rowTmpl string, iterator []T, o ...option.Opt) error {
	return table.Table[T](w, rowTmpl, iterator, o...)
}

func TableAuto[T any](w io.Writer, iterator []T, o ...option.Opt) error {
	return table.TableAuto[T](w, iterator, o...)
}

func Facts[T any](w io.Writer, data T, o ...option.Opt) error {
	return table.Facts[T](w, data, o...)
}

func WithColumnTypeFormat[T any](fn func(v T) string) option.Opt {
	return table.WithColumnTypeFormat[T](fn)
}

func WithColumnFormat[T any](name string, fn func(v T) string) option.Opt {
	return table.WithColumnFormat[T](name, fn)
}

func WithColumnTemplate(name, columnTmpl string) option.Opt {
	return table.WithColumnTemplate(name, columnTmpl)
}

func WithIncludeColumns(names ...string) option.Opt {
	return table.WithIncludeColumns(names...)
}

func WithSkipColumns(names ...string) option.Opt {
	return table.WithSkipColumns(names...)
}

// WithMaxWidth caps the total rendered row width and cell padding included.
func WithMaxWidth(chars int) option.Opt {
	return table.WithMaxWidth(chars)
}

// WithMultilineCells renders line breaks inside cells as aligned physical rows.
// Without it, line breaks inside cells are rendered as spaces.
func WithMultilineCells() option.Opt {
	return table.WithMultilineCells()
}

func WithFloat64AsPercent() option.Opt {
	return table.WithFloat64AsPercent()
}

func WithFloat64AsPercentPositiveColored() option.Opt {
	return table.WithFloat64AsPercentPositiveColored()
}

// WithColumnGreenRedScale colors an auto-template column using a 5-step
// scale from green (lowest ranked values) to red (highest ranked values),
// based on unique seen-so-far values. For TableIter, previously rendered
// rows are not recalculated.
func WithColumnGreenRedScale(name string) option.Opt {
	return table.WithColumnGreenRedScale(name)
}

// WithColumnRedGreenScale colors an auto-template column using a 5-step
// scale from red (lowest ranked values) to green (highest ranked values),
// based on unique seen-so-far values. For TableIter, previously rendered
// rows are not recalculated.
func WithColumnRedGreenScale(name string) option.Opt {
	return table.WithColumnRedGreenScale(name)
}
