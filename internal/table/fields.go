// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package table

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

var (
	timeType         = reflect.TypeFor[time.Time]()
	timeDurationType = reflect.TypeFor[time.Duration]()
	fmtStringerType  = reflect.TypeFor[fmt.Stringer]()
)

func (t *table) fieldValueByPath(v any, path string) (any, bool) {
	raw, ok := t.scalarValue(v)
	if !ok {
		return nil, false
	}
	for _, name := range strings.Split(path, ".") {
		if raw.Kind() != reflect.Struct {
			return nil, false
		}
		field := raw.FieldByName(name)
		if !field.IsValid() {
			return nil, false
		}
		for field.Kind() == reflect.Pointer {
			if field.IsNil() {
				return nil, false
			}
			field = field.Elem()
		}
		raw = field
	}
	if !raw.IsValid() || !raw.CanInterface() {
		return nil, false
	}
	return raw.Interface(), true
}

// theoretically we could make this public with additional options.
type fieldMetadata struct {
	header     string
	name       string
	autoHeader bool
	alignRight bool
	stringer   bool
	kind       reflect.Kind
	typ        reflect.Type
	pointer    bool
}

func (f *fieldMetadata) Template() string {
	if f.stringer {
		return "{{tableString ." + f.name + "}}"
	}
	switch f.kind {
	case reflect.Bool:
		return "{{if ." + f.name + "}}yes{{else}}no{{end}}"
	case reflect.Float32, reflect.Float64:
		return "{{printf \"%.2f\" ." + f.name + "}}"
	default:
		return "{{." + f.name + "}}"
	}
}

type structFields []*fieldMetadata

func (s structFields) Template() string {
	parts := make([]string, len(s))
	for i, col := range s {
		parts[i] = col.Template()
	}

	return strings.Join(parts, "\t")
}

// structFieldsForType reflects columns from static type information only, so
// interface element types are rejected: there is no value to infer a schema from.
func structFieldsForType(rt reflect.Type) (structFields, error) {
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() == reflect.Interface {
		return nil, fmt.Errorf("interface type %s is not supported, use a concrete struct type", rt)
	}
	if rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct or pointer to struct, got %s", rt.Kind())
	}

	return reflectStructFields(rt, map[reflect.Type]struct{}{})
}

//nolint:cyclop // TODO: fix
func reflectStructFields(rt reflect.Type, stack map[reflect.Type]struct{}) (structFields, error) {
	_, ok := stack[rt]
	if ok {
		return nil, nil
	}
	stack[rt] = struct{}{}
	defer delete(stack, rt)
	var out structFields
	for f := range rt.Fields() {
		if !f.IsExported() {
			continue
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = f.Type.Elem()
		}
		isStringer := typeImplementsStringer(ft)
		if ft.Kind() == reflect.Struct && !isStringer {
			nested, err := reflectStructFields(ft, stack)
			if err != nil {
				return nil, fmt.Errorf("nested %s: %w", f.Name, err)
			}
			for _, n := range nested {
				if n.autoHeader {
					n.header = strings.ToUpper(f.Name + "." + n.header)
				}
				n.name = f.Name + "." + n.name
				out = append(out, n)
			}
			continue
		}
		meta, err := reflectFieldMetadata(ft, f.Tag, f.Name)
		if errors.Is(err, errCannotUse) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		meta.pointer = f.Type.Kind() == reflect.Pointer
		out = append(out, meta)
	}

	return out, nil
}

var errCannotUse = errors.New("cannot use")

func reflectFieldMetadata(ft reflect.Type, tag reflect.StructTag, name string) (*fieldMetadata, error) {
	meta := fieldMetadata{
		autoHeader: tag.Get("header") == "",
		kind:       ft.Kind(),
		name:       name,
		stringer:   typeImplementsStringer(ft),
		typ:        ft,
	}
	switch ft.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		meta.alignRight = true
	case reflect.Bool, reflect.String:
		meta.kind = ft.Kind()
	default:
		if meta.autoHeader && !meta.stringer {
			return nil, fmt.Errorf("%w %s without explicit header tag", errCannotUse, ft.Kind())
		}
	}
	if tag.Get("header") == "" {
		meta.header = strings.ToUpper(name)
	} else {
		queue := strings.Split(tag.Get("header"), ",")
		meta.header = queue[0]
		queue = queue[1:]
		for len(queue) > 0 {
			option := strings.TrimSpace(queue[0])
			queue = queue[1:]
			switch option {
			case "align-right":
				meta.alignRight = true
			case "align-left":
				meta.alignRight = false
			default:
				return nil, fmt.Errorf("unknown header option: %q", option)
			}
		}
	}

	return &meta, nil
}

func typeImplementsStringer(typ reflect.Type) bool {
	if typ == nil {
		return false
	}
	if typ.Implements(fmtStringerType) {
		return true
	}
	return typ.Kind() != reflect.Pointer && reflect.PointerTo(typ).Implements(fmtStringerType)
}
