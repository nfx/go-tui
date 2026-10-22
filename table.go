// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/option"
	"io"
	"iter"
	"reflect"
	"strings"
)

// TableIter renders rows as the iterator yields them, writing every ten.
// Column widths lock after the first batch, scales rank the values seen so
// far, and written rows are never repainted. The theme and the writer's
// capabilities are captured before the first row, and hold for every batch.
func TableIter[T any](w io.Writer, rowTmpl string, iterator iter.Seq2[T, error], o ...option.Opt) error {
	t, err := newTable[T](w, rowTmpl, o...)
	if err != nil {
		return fmt.Errorf("table: %w", err)
	}
	for v, err := range iterator {
		if err != nil {
			return fmt.Errorf("row %d: fetch: %w", t.consumed, err)
		}
		err = t.Append(v)
		if err != nil {
			return fmt.Errorf("row %d: append: %w", t.consumed, err)
		}
	}

	return t.flush(true)
}

func Table[T any](w io.Writer, rowTmpl string, iterator []T, o ...option.Opt) error {
	t, err := newTable[T](w, rowTmpl, o...)
	if err != nil {
		return fmt.Errorf("table: %w", err)
	}
	for _, v := range iterator {
		err = t.Append(v)
		if err != nil {
			return fmt.Errorf("row %d: append: %w", t.consumed, err)
		}
	}

	return t.flush(true)
}

func TableAuto[T any](w io.Writer, iterator []T, o ...option.Opt) error {
	t, err := newTable[T](w, "", o...)
	if err != nil {
		return fmt.Errorf("table: %w", err)
	}
	for _, v := range iterator {
		err = t.Append(v)
		if err != nil {
			return fmt.Errorf("row %d: append: %w", t.consumed, err)
		}
	}

	return t.flush(true)
}

func Facts[T any](w io.Writer, data T, o ...option.Opt) error {
	options := make([]option.Opt, 0, len(o)+1)
	options = append(options, option.OpT(func(t *table) error {
		t.suppressHeaders = true
		return nil
	}))
	options = append(options, o...)

	rt := reflect.TypeFor[T]()
	if rt.Kind() == reflect.Interface {
		// interface-typed input has no static schema; use the supplied value
		if any(data) == nil {
			return errors.New("facts: nil value")
		}
		rt = reflect.TypeOf(data)
	}
	t, err := newTableFor(rt, w, "", options...)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	f := &facts{table: t}
	maxWidth := f.factsMaxWidth(w)
	err = f.renderFacts(data, maxWidth)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

func WithColumnTypeFormat[T any](fn func(v T) string) option.Opt {
	return option.OpT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		if fn == nil {
			return errors.New("column type formatter cannot be nil")
		}
		valueType := reflect.TypeFor[T]()
		funcName := t.registerTemplateFunc("tableColumnTypeFormat", fn)
		t.ensureAutoTemplateMaps()
		t.columnTypeFormats[valueType] = funcName
		return nil
	})
}

func WithColumnFormat[T any](name string, fn func(v T) string) option.Opt {
	return option.OpT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		if fn == nil {
			return errors.New("column formatter cannot be nil")
		}
		meta, err := t.columnMetadata(name)
		if err != nil {
			return err
		}
		valueType := reflect.TypeFor[T]()
		if meta.typ != valueType {
			return fmt.Errorf("column %q has type %s, formatter expects %s", name, meta.typ, valueType)
		}
		funcName := t.registerTemplateFunc("tableColumnNameFormat", fn)
		t.ensureAutoTemplateMaps()
		t.columnNameFormats[name] = funcName
		return nil
	})
}

func WithColumnTemplate(name, columnTmpl string) option.Opt {
	return option.OpT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		_, err := t.columnMetadata(name)
		if err != nil {
			return err
		}
		if strings.TrimSpace(columnTmpl) == "" {
			return fmt.Errorf("column %q template cannot be empty", name)
		}
		t.ensureAutoTemplateMaps()
		t.columnTemplates[name] = columnTmpl
		return nil
	})
}

func WithIncludeColumns(names ...string) option.Opt {
	return option.OpT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		set, err := t.columnSet(names)
		if err != nil {
			return err
		}
		for name := range set {
			skipped, exists := t.skipColumns[name]
			if exists && skipped {
				return fmt.Errorf("column %q cannot be included and skipped", name)
			}
		}
		t.ensureAutoTemplateMaps()
		t.includeColumns = set
		return nil
	})
}

func WithSkipColumns(names ...string) option.Opt {
	return option.OpT(func(t *table) error {
		if !t.autoTemplate {
			return nil
		}
		set, err := t.columnSet(names)
		if err != nil {
			return err
		}
		for name := range set {
			included, exists := t.includeColumns[name]
			if exists && included {
				return fmt.Errorf("column %q cannot be included and skipped", name)
			}
		}
		t.ensureAutoTemplateMaps()
		t.skipColumns = set
		return nil
	})
}

// WithMaxWidth caps the total rendered row width and cell padding included.
func WithMaxWidth(chars int) option.Opt {
	return option.OpT(func(t *table) error {
		if chars <= 0 {
			return fmt.Errorf("%w: max width must be greater than 0", ErrInvalidState)
		}
		t.maxWidth = chars
		t.maxWidthExplicit = true
		return nil
	})
}

// WithMultilineCells renders line breaks inside cells as aligned physical rows.
// Without it, line breaks inside cells are rendered as spaces.
func WithMultilineCells() option.Opt {
	return option.OpT(func(t *table) error {
		t.multilineCells = true
		return nil
	})
}

func WithFloat64AsPercent() option.Opt {
	return WithColumnTypeFormat(func(v float64) string {
		return percentString(v)
	})
}

func WithFloat64AsPercentPositiveColored() option.Opt {
	return option.OpT(func(t *table) error {
		return WithColumnTypeFormat(t.signedPercent)(t)
	})
}

// WithColumnGreenRedScale colors an auto-template column using a 5-step
// scale from green (lowest ranked values) to red (highest ranked values),
// based on unique seen-so-far values. For TableIter, previously rendered
// rows are not recalculated.
func WithColumnGreenRedScale(name string) option.Opt {
	return option.OpT(func(t *table) error {
		return t.setColumnScale(name, false)
	})
}

// WithColumnRedGreenScale colors an auto-template column using a 5-step
// scale from red (lowest ranked values) to green (highest ranked values),
// based on unique seen-so-far values. For TableIter, previously rendered
// rows are not recalculated.
func WithColumnRedGreenScale(name string) option.Opt {
	return option.OpT(func(t *table) error {
		return t.setColumnScale(name, true)
	})
}
