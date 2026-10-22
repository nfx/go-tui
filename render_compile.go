// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"text/template"
	"unicode"
)

// withCustom adds custom helpers to builtins in place.
func (t Theme) withCustom(builtins template.FuncMap) template.FuncMap {
	maps.Copy(builtins, t.Funcs)
	return builtins
}

// compile validates an independent theme copy without terminal I/O or executing user code.
func (in Theme) compile() (*compiledTheme, error) {
	t := in.clone()
	var err error
	c := &compiledTheme{}
	c.palette, err = t.Palette.parse()
	if err != nil {
		return nil, fmt.Errorf("palette: %w", err)
	}
	t.Palette = t.Palette.canonical()
	t.Symbols, err = t.Symbols.normalize()
	if err != nil {
		return nil, fmt.Errorf("symbols: %w", err)
	}
	t.Spinner, err = t.Spinner.normalize()
	if err != nil {
		return nil, err
	}
	t.Progress, err = t.Progress.normalize()
	if err != nil {
		return nil, err
	}
	t.Table, err = t.Table.normalize()
	if err != nil {
		return nil, err
	}
	t.JSON, err = t.JSON.normalize()
	if err != nil {
		return nil, err
	}
	builtins := destination{symbols: t.Symbols}.funcs()
	err = in.compileFuncs(builtins, t.Funcs)
	if err != nil {
		return nil, err
	}
	funcs := t.withCustom(builtins)
	sources := t.Progress.builtins()
	base, err := sources.compile(funcs, templates{})
	if err != nil {
		return nil, fmt.Errorf("built-in %w", err)
	}
	c.templates, err = t.Templates.compile(funcs, base)
	if err != nil {
		return nil, err
	}
	c.source = t
	return c, nil
}

// compileFuncs reports custom helper validation errors by name.
func (in Theme) compileFuncs(builtins, funcs template.FuncMap) error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(funcs)) {
		if err := in.checkFunc(builtins, name, funcs[name]); err != nil {
			errs = append(errs, fmt.Errorf("theme.Funcs.%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// widgetHelpers are the names of helpers that widgets add to the builtins.
var widgetHelpers = []string{"tableString"}

// generatedHelperPrefixes are the prefixes of names that tables generate.
var generatedHelperPrefixes = []string{"__", "tableColumnTypeFormat", "tableColumnNameFormat"}

// checkFunc validates helper names and signatures without executing user code.
func (in Theme) checkFunc(builtins template.FuncMap, name string, fn any) error {
	if !in.isHelperName(name) {
		return errors.New("not a template identifier")
	}
	if _, ok := builtins[name]; ok || slices.Contains(widgetHelpers, name) {
		return errors.New("reserved for a built-in helper")
	}
	for _, prefix := range generatedHelperPrefixes {
		if strings.HasPrefix(name, prefix) {
			return fmt.Errorf("prefix %q is reserved for generated helpers", prefix)
		}
	}
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func || v.IsNil() {
		return fmt.Errorf("want a function, got %T", fn)
	}
	t := v.Type()
	switch {
	case t.NumOut() == 1:
	case t.NumOut() == 2 && t.Out(1) == reflect.TypeFor[error]():
	default:
		return fmt.Errorf("%s: want one result, or a result and an error", t)
	}
	return nil
}

// isHelperName reports whether name is a template function identifier.
func (in Theme) isHelperName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_':
		case i == 0 && !unicode.IsLetter(r):
			return false
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			return false
		}
	}
	return true
}
