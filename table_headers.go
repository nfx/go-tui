// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"text/template"
)

func (t *table) headers() error {
	if t.autoTemplate {
		return t.autoHeaders()
	}
	headers, err := t.extractFromNode(t.tmpl.Root)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	lookup := make(map[string]*fieldMetadata, len(t.metadata))
	for _, f := range t.metadata {
		lookup[f.name] = f
	}
	t.columns = make([]tableColumn, len(headers))
	columns := make([]tableColumnInfo, len(headers))
	bold := make([]Text, len(headers))
	for i := range headers {
		meta, ok := lookup[headers[i]]
		if !ok {
			meta = &fieldMetadata{
				header:     strings.ToUpper(headers[i]),
				name:       headers[i],
				kind:       reflect.String,
				autoHeader: true,
			}
		}
		t.columns[i].meta = meta
		columns[i] = tableColumnInfo{
			Header: meta.header,
			Kind:   meta.kind.String(),
		}
		if !t.headerRow() {
			continue
		}
		bold[i], err = t.heading(meta)
		if err != nil {
			return err
		}
	}
	if !t.headerRow() {
		return nil
	}
	if t.eventSink != nil {
		t.emit(tableBegin{Columns: columns})
	} else {
		t.addRow(bold)
	}

	return nil
}

// autoHeaders emits headers directly from reflected metadata in auto-template mode.
func (t *table) autoHeaders() error {
	t.columns = make([]tableColumn, len(t.metadata))
	columns := make([]tableColumnInfo, len(t.metadata))
	headers := make([]Text, len(t.metadata))
	for i, meta := range t.metadata {
		t.columns[i].meta = meta
		columns[i] = tableColumnInfo{
			Header: meta.header,
			Kind:   meta.kind.String(),
		}
		if !t.headerRow() {
			continue
		}
		var err error
		headers[i], err = t.heading(meta)
		if err != nil {
			return err
		}
	}
	if !t.headerRow() {
		return nil
	}
	if t.eventSink != nil {
		t.emit(tableBegin{Columns: columns})
		return nil
	}
	t.addRow(headers)
	return nil
}

// autoRowTemplate applies include/skip filters and format rules to reflected fields.
func (t *table) autoRowTemplate() string {
	fields := t.filteredFields()
	t.metadata = fields
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = t.columnRenderTemplate(field)
	}
	return strings.Join(parts, "\t")
}

// filteredFields applies include/skip selectors while preserving struct field order.
func (t *table) filteredFields() structFields {
	if len(t.includeColumns) == 0 && len(t.skipColumns) == 0 {
		return t.metadata
	}
	out := make(structFields, 0, len(t.metadata))
	for _, field := range t.metadata {
		if len(t.includeColumns) > 0 {
			included, ok := t.includeColumns[field.name]
			if !ok || !included {
				continue
			}
		}
		skip, ok := t.skipColumns[field.name]
		if ok && skip {
			continue
		}
		out = append(out, field)
	}
	return out
}

// columnRenderTemplate resolves per-column template, name formatter, type formatter, then default.
func (t *table) columnRenderTemplate(meta *fieldMetadata) string {
	tmpl, ok := t.columnTemplates[meta.name]
	if ok {
		return tmpl
	}
	fnName, ok := t.columnNameFormats[meta.name]
	if ok {
		return t.formatterTemplate(fnName, meta)
	}
	fnName, ok = t.columnTypeFormats[meta.typ]
	if ok {
		return t.formatterTemplate(fnName, meta)
	}
	return meta.Template()
}

// formatterTemplate calls the formatter, rendering nil pointer fields as empty
// because text/template cannot dereference nil before the formatter runs.
func (t *table) formatterTemplate(fnName string, meta *fieldMetadata) string {
	call := "{{" + fnName + " ." + meta.name + "}}"
	if meta.pointer {
		return "{{if ." + meta.name + "}}" + call + "{{end}}"
	}
	return call
}

// templateFuncs merges shared color functions with per-table formatter
// functions, bound to the writer of the table.
func (t *table) templateFuncs() template.FuncMap {
	shared := t.look.funcs
	funcs := make(template.FuncMap, len(shared)+len(t.customTemplateFuncs)+2)
	maps.Copy(funcs, shared)
	funcs["tableString"] = tableString
	funcs["__cellSep"] = t.cellSeparator
	for name, fn := range t.customTemplateFuncs {
		funcs[name] = fn
	}
	return funcs
}

// registerTemplateFunc stores a formatter under a unique name.
func (t *table) registerTemplateFunc(prefix string, fn any) string {
	t.ensureAutoTemplateMaps()
	name := fmt.Sprintf("%s%d", prefix, t.templateFuncSeq)
	t.templateFuncSeq++
	t.customTemplateFuncs[name] = fn
	return name
}

// ensureAutoTemplateMaps initializes lazy maps used by auto-template options.
func (t *table) ensureAutoTemplateMaps() {
	if t.customTemplateFuncs == nil {
		t.customTemplateFuncs = template.FuncMap{}
	}
	if t.columnTypeFormats == nil {
		t.columnTypeFormats = map[reflect.Type]string{}
	}
	if t.columnNameFormats == nil {
		t.columnNameFormats = map[string]string{}
	}
	if t.columnTemplates == nil {
		t.columnTemplates = map[string]string{}
	}
	if t.includeColumns == nil {
		t.includeColumns = map[string]bool{}
	}
	if t.skipColumns == nil {
		t.skipColumns = map[string]bool{}
	}
	if t.columnScales == nil {
		t.columnScales = map[string]*columnScale{}
	}
}

// columnMetadata resolves and validates a struct field path used by column options.
func (t *table) columnMetadata(name string) (*fieldMetadata, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("column name cannot be empty")
	}
	for _, meta := range t.metadata {
		if meta.name == name {
			return meta, nil
		}
	}
	return nil, fmt.Errorf("unknown column %q", name)
}

// columnSet validates column names and returns a set.
func (t *table) columnSet(names []string) (map[string]bool, error) {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		_, err := t.columnMetadata(name)
		if err != nil {
			return nil, err
		}
		set[name] = true
	}
	return set, nil
}
