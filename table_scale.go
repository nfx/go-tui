// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

// signedPercent formats v as a percentage, positive in the success role and
// negative in the error role: a sign is data, not an outcome, so only this
// option colors it.
func (t *table) signedPercent(v float64) string {
	out := percentString(v)
	var role ansi.Text
	switch {
	case v > 0:
		role = t.look.Roles.Success
	case v < 0:
		role = t.look.Roles.Failure
	}
	return t.look.Style(role, ansi.Text(out)).String()
}

// percentString scales values to percentages and truncates toward zero.
func percentString(v float64) string {
	scaled := v * 10000
	scaled = math.Nextafter(scaled, math.Copysign(math.Inf(1), scaled))
	value := math.Trunc(scaled) / 100
	if value == 0 {
		return "0%"
	}
	if math.Abs(value) >= 10 {
		return fmt.Sprintf("%.0f%%", math.Trunc(value))
	}
	out := fmt.Sprintf("%.2f%%", value)
	return strings.Replace(out, ".00%", "%", 1)
}

type greenRedScalarFn func(v any) (float64, bool)

type columnScale struct {
	toScalar    greenRedScalarFn
	palette     []string
	roundFloats bool
	unique      []float64
}

func (s *columnScale) color(v any) string {
	scalar, ok := s.scalar(v)
	if !ok {
		return ""
	}
	scalar = s.normalize(scalar)
	rank, total := s.observeRank(scalar)
	if total < 2 {
		return ""
	}
	if len(s.palette) == 0 {
		return ""
	}
	return s.palette[s.bucketIndex(rank, total)]
}

func (s *columnScale) scalar(v any) (float64, bool) {
	if s.toScalar == nil {
		return 0, false
	}
	scalar, ok := s.toScalar(v)
	if !ok || math.IsNaN(scalar) || math.IsInf(scalar, 0) {
		return 0, false
	}
	return scalar, true
}

func (s *columnScale) normalize(scalar float64) float64 {
	if !s.roundFloats {
		return scalar
	}
	const factor = 10000.0
	return math.Round(scalar*factor) / factor
}

func (s *columnScale) observeRank(scalar float64) (rank, total int) {
	idx := sort.SearchFloat64s(s.unique, scalar)
	if idx >= len(s.unique) || s.unique[idx] != scalar {
		s.unique = append(s.unique, 0)
		copy(s.unique[idx+1:], s.unique[idx:])
		s.unique[idx] = scalar
	}
	return idx, len(s.unique)
}

func (s *columnScale) bucketIndex(rank, total int) int {
	if total < 2 {
		return 0
	}
	if len(s.palette) < 2 {
		return 0
	}
	numerator := rank * (len(s.palette) - 1)
	denominator := total - 1
	idx := int(math.Round(float64(numerator) / float64(denominator)))
	if idx < 0 {
		return 0
	}
	if idx >= len(s.palette) {
		return len(s.palette) - 1
	}
	return idx
}

// scalePalette is the color of each rank bucket: green to red, or red to
// green when reversed, derived from the success, foreground and error roles.
// It keeps the five buckets even where colors coincide, so a rank means the
// same on every palette.
func (t *table) scalePalette(reversed bool) []string {
	var palette []string
	for _, c := range t.look.Diverging() {
		palette = append(palette, c.String())
	}
	if reversed {
		slices.Reverse(palette)
	}
	return palette
}

func (t *table) setColumnScale(name string, reversed bool) error {
	if !t.autoTemplate {
		return nil
	}
	meta, err := t.columnMetadata(name)
	if err != nil {
		return err
	}
	toScalar, err := t.scaleScalar(meta.typ, meta.kind)
	if err != nil {
		return fmt.Errorf("column %q: %w", name, err)
	}
	t.ensureAutoTemplateMaps()
	t.columnScales[name] = &columnScale{
		toScalar:    toScalar,
		palette:     t.scalePalette(reversed),
		roundFloats: t.isFloatKind(meta.kind),
	}
	return nil
}

func (t *table) scaleScalar(typ reflect.Type, kind reflect.Kind) (greenRedScalarFn, error) {
	if typ == timeType {
		return t.scaleTimeScalar, nil
	}
	if typ == timeDurationType {
		return t.scaleDurationScalar, nil
	}
	return t.scaleNumericScalar(kind, typ)
}

func (t *table) scaleNumericScalar(kind reflect.Kind, typ reflect.Type) (greenRedScalarFn, error) {
	if t.isIntKind(kind) {
		return t.scaleIntScalar, nil
	}
	if t.isUintKind(kind) {
		return t.scaleUintScalar, nil
	}
	if t.isFloatKind(kind) {
		return t.scaleFloatScalar, nil
	}
	return nil, fmt.Errorf("unsupported type %s for green-red scale", typ)
}

func (t *table) scaleTimeScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok {
		return 0, false
	}
	ts, ok := raw.Interface().(time.Time)
	if !ok {
		return 0, false
	}
	return float64(ts.UnixNano()), true
}

func (t *table) scaleDurationScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok {
		return 0, false
	}
	d, ok := raw.Interface().(time.Duration)
	if !ok {
		return 0, false
	}
	return float64(d), true
}

func (t *table) scaleIntScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok || !t.isIntKind(raw.Kind()) {
		return 0, false
	}
	return float64(raw.Int()), true
}

func (t *table) scaleUintScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok || !t.isUintKind(raw.Kind()) {
		return 0, false
	}
	return float64(raw.Uint()), true
}

func (t *table) scaleFloatScalar(v any) (float64, bool) {
	raw, ok := t.scalarValue(v)
	if !ok || !t.isFloatKind(raw.Kind()) {
		return 0, false
	}
	return raw.Float(), true
}

func (t *table) scalarValue(v any) (reflect.Value, bool) {
	if v == nil {
		return reflect.Value{}, false
	}
	raw := reflect.ValueOf(v)
	for raw.Kind() == reflect.Pointer {
		if raw.IsNil() {
			return reflect.Value{}, false
		}
		raw = raw.Elem()
	}
	return raw, true
}

func (*table) isFloatKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func (*table) isIntKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	default:
		return false
	}
}

func (*table) isUintKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}

// scaleColors observes the record's scaled values once and returns a color per
// column, or nil when no scale is configured.
func (t *table) scaleColors(v any) []string {
	if len(t.columnScales) == 0 {
		return nil
	}
	colors := make([]string, len(t.columns))
	for i := range t.columns {
		meta := t.columns[i].meta
		scale, ok := t.columnScales[meta.name]
		if !ok || scale == nil {
			continue
		}
		raw, ok := t.fieldValueByPath(v, meta.name)
		if !ok {
			continue
		}
		colors[i] = scale.color(raw)
	}
	return colors
}
