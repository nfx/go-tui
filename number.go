// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

type numberType interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

var (
	errNumberBelowZero = errors.New("value cannot go below zero")
	errNumberTooSmall  = errors.New("step is too small for this type; choose a larger digit")
)

// Number prompts for a decimal number of explicit type T (excluding uintptr).
// Invalid or out-of-range input stays editable. Errors return T's zero value.
//
// Up/Down step integers by one and floats at the caret's decimal place.
// Steps preserve fractional digits and reject overflow or invisible changes.
//
// Only [WithDefault], [WithAtLeast], [WithAtMost], [WithInput] and [WithOutput] apply.
// Bounds are inclusive: typed values outside them are rejected on Enter, and
// Up/Down refuse to step past them.
func Number[T numberType](ctx context.Context, label string, option ...opt) (T, error) {
	n := newNumber[T](ctx, label)
	return n.read(ctx, option...)
}

// number adds conversion and stepping to input.
type number[T numberType] struct {
	prompt *input
	kind   reflect.Kind
	bits   int
	float  bool
	signed bool
	// value holds the last successful conversion.
	value    T
	min, max *numberBound
}

// numberBound is an inclusive limit, compared exactly.
type numberBound struct {
	rat  *big.Rat
	text string
}

type withNumberBounds interface {
	setMin(b *numberBound)
	setMax(b *numberBound)
}

// WithAtLeast rejects [Number] values below v. The bound may have any numeric
// type; it is compared exactly and, for floats, at the precision of the answer.
func WithAtLeast[B numberType](v B) opt {
	return opT(func(x withNumberBounds) error {
		b, err := newNumberBound(v)
		if err != nil {
			return err
		}
		x.setMin(b)
		return nil
	})
}

// WithAtMost rejects [Number] values above v. See [WithAtLeast].
func WithAtMost[B numberType](v B) opt {
	return opT(func(x withNumberBounds) error {
		b, err := newNumberBound(v)
		if err != nil {
			return err
		}
		x.setMax(b)
		return nil
	})
}

// newNumberBound keeps a finite limit and its display text.
func newNumberBound[B numberType](v B) (*numberBound, error) {
	rat, text := numberRat(v)
	if rat == nil {
		return nil, fmt.Errorf("%w: bound must be finite", ErrInvalidOption)
	}
	return &numberBound{rat: rat, text: text}, nil
}

// numberRat converts v exactly; it returns nil for NaN and infinities.
func numberRat[B numberType](v B) (*big.Rat, string) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() { //nolint:exhaustive // numberType has only these kinds
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		return new(big.Rat).SetFloat64(f), strconv.FormatFloat(f, 'f', -1, rv.Type().Bits())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		return new(big.Rat).SetInt(new(big.Int).SetUint64(u)), strconv.FormatUint(u, 10)
	}
	i := rv.Int()
	return new(big.Rat).SetInt64(i), strconv.FormatInt(i, 10)
}

// setDefault seeds the editable number text.
func (n *number[T]) setDefault(d any) { n.prompt.setDefault(n.format(d)) }

// format writes numeric values in plain decimal notation at their own width,
// and anything else as text, which the numeric rules then judge.
func (n *number[T]) format(d any) string {
	if d != nil {
		rv := reflect.ValueOf(d)
		switch rv.Kind() { //nolint:exhaustive // other kinds use the text form
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.FormatInt(rv.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return strconv.FormatUint(rv.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			return strconv.FormatFloat(rv.Float(), 'f', -1, rv.Type().Bits())
		}
	}
	return fmt.Sprint(d)
}

// setReader routes prompt input through r.
func (n *number[T]) setReader(r io.Reader) { n.prompt.setReader(r) }

// setWriter routes prompt output through w.
func (n *number[T]) setWriter(w io.Writer) { n.prompt.setWriter(w) }

// setMin records the inclusive lower limit.
func (n *number[T]) setMin(b *numberBound) { n.min = b }

// setMax records the inclusive upper limit.
func (n *number[T]) setMax(b *numberBound) { n.max = b }

// checkBounds fits the bounds to T and rejects empty ranges.
func (n *number[T]) checkBounds() error {
	for _, b := range []*numberBound{n.min, n.max} {
		if b == nil || !n.float {
			continue
		}
		// a float answer cannot be closer to the bound than T allows
		f, _ := b.rat.Float64()
		r := new(big.Rat).SetFloat64(float64(T(f)))
		if r == nil {
			return fmt.Errorf("%w: bound %s is out of range for %s", ErrInvalidOption, b.text, n.kind)
		}
		b.rat = r
	}
	if !n.float {
		// a bound may exceed T as long as part of T's range remains
		lo, hi := n.intRange()
		if n.min != nil && n.min.rat.Cmp(new(big.Rat).SetInt(hi)) > 0 {
			return fmt.Errorf("%w: min %s is above the largest %s", ErrInvalidOption, n.min.text, n.kind)
		}
		if n.max != nil && n.max.rat.Cmp(new(big.Rat).SetInt(lo)) < 0 {
			return fmt.Errorf("%w: max %s is below the smallest %s", ErrInvalidOption, n.max.text, n.kind)
		}
	}
	if n.min != nil && n.max != nil && n.min.rat.Cmp(n.max.rat) > 0 {
		return fmt.Errorf("%w: min %s is above max %s", ErrInvalidOption, n.min.text, n.max.text)
	}
	return nil
}

// intRange returns the smallest and largest value of an integer T.
func (n *number[T]) intRange() (lo, hi *big.Int) {
	one := big.NewInt(1)
	if n.signed {
		hi = new(big.Int).Lsh(one, uint(n.bits-1))
		lo = new(big.Int).Neg(hi)
		return lo, hi.Sub(hi, one)
	}
	hi = new(big.Int).Lsh(one, uint(n.bits))
	return big.NewInt(0), hi.Sub(hi, one)
}

// cmp compares a typed value with a bound without losing precision.
func (n *number[T]) cmp(v T, b *numberBound) int {
	r, _ := numberRat(v)
	return r.Cmp(b.rat)
}

// below reports whether v violates the lower limit.
func (n *number[T]) below(v T) bool { return n.min != nil && n.cmp(v, n.min) < 0 }

// above reports whether v violates the upper limit.
func (n *number[T]) above(v T) bool { return n.max != nil && n.cmp(v, n.max) > 0 }

// boundsError describes the configured range for correction.
func (n *number[T]) boundsError() error {
	switch {
	case n.min != nil && n.max != nil:
		return fmt.Errorf("value must be between %s and %s", n.min.text, n.max.text)
	case n.min != nil:
		return fmt.Errorf("value must be at least %s", n.min.text)
	}
	return fmt.Errorf("value must be at most %s", n.max.text)
}

// newNumber wires type-aware validation and stepping into the prompt.
func newNumber[T numberType](ctx context.Context, label string) *number[T] {
	typ := reflect.TypeFor[T]()
	kind := typ.Kind()
	n := &number[T]{
		prompt: newInput(ctx, label),
		kind:   kind,
		bits:   typ.Bits(),
		float:  kind == reflect.Float32 || kind == reflect.Float64,
	}
	n.signed = n.float || (kind >= reflect.Int && kind <= reflect.Int64)
	// the numeric rule goes first, so that options appended later cannot bypass it
	n.prompt.numeric = true
	n.prompt.rules = append(n.prompt.rules, n.validate)
	n.prompt.adjust = n.adjust
	return n
}

// read checks options before prompting and returns the confirmed value.
func (n *number[T]) read(ctx context.Context, option ...opt) (T, error) {
	var zero T
	if err := opts(option).Apply(n); err != nil {
		return zero, err
	}
	if err := n.checkBounds(); err != nil {
		return zero, err
	}
	if _, err := n.prompt.read(ctx); err != nil {
		return zero, err
	}
	return n.value, nil
}

// validate stores the confirmed value.
func (n *number[T]) validate(_ context.Context, s string) error {
	var zero T
	n.value = zero
	v, err := n.convert(s)
	if err != nil {
		return err
	}
	if n.below(v) || n.above(v) {
		return n.boundsError()
	}
	n.value = v
	return nil
}

// syntax accepts only plain decimal notation.
func (n *number[T]) syntax(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || (s[i] == '-' && n.signed)) {
		i++
	}
	digits := func() int {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return i - start
	}
	whole := digits()
	if !n.float {
		return whole > 0 && i == len(s)
	}
	if i < len(s) && s[i] == '.' {
		i++
		frac := digits()
		return i == len(s) && (whole > 0 || frac > 0)
	}
	return whole > 0 && i == len(s)
}

// syntaxError gives an input hint suited to T.
func (n *number[T]) syntaxError() error {
	switch {
	case n.float:
		return errors.New("enter a decimal number, like 12.5")
	case n.signed:
		return errors.New("enter a whole number")
	}
	return errors.New("enter a non-negative whole number")
}

// rangeError describes the values T can represent.
func (n *number[T]) rangeError() error {
	switch {
	case n.float:
		return fmt.Errorf("value is out of range for %s", n.kind)
	case n.signed:
		hi := int64(math.MaxInt64) >> (64 - n.bits)
		return fmt.Errorf("value must be between %d and %d", -hi-1, hi)
	}
	return fmt.Errorf("value must be between 0 and %d", uint64(math.MaxUint64)>>(64-n.bits))
}

// convert parses at T's width with range checks.
func (n *number[T]) convert(s string) (T, error) {
	var zero T
	if s == "" {
		return zero, errors.New("a number is required")
	}
	if !n.syntax(s) {
		return zero, n.syntaxError()
	}
	switch {
	case n.float:
		f, err := strconv.ParseFloat(s, n.bits)
		if err != nil {
			return zero, n.rangeError()
		}
		if f == 0 && strings.ContainsAny(s, "123456789") {
			return zero, fmt.Errorf("value is too small for %s", n.kind)
		}
		return T(f), nil
	case n.signed:
		i, err := strconv.ParseInt(s, 10, n.bits)
		if err != nil {
			return zero, n.rangeError()
		}
		return T(i), nil
	}
	u, err := strconv.ParseUint(strings.TrimPrefix(s, "+"), 10, n.bits)
	if err != nil {
		return zero, n.rangeError()
	}
	return T(u), nil
}

// adjust steps without changing rejected drafts.
func (n *number[T]) adjust(key rune) error {
	p := n.prompt
	up := key == '↑'
	text := p.typed
	if text == "" {
		next := "1"
		if !up {
			next = "-1"
		}
		return n.commit(text, next, up, func() int { return len(next) })
	}
	if !n.syntax(text) {
		return errors.New("fix the number before using the arrows")
	}
	if _, err := n.convert(text); err != nil {
		return fmt.Errorf("%w; edit it before using the arrows", err)
	}
	caret := min(p.cursor, len(text))
	exp, kind := 0, caretDigit
	if n.float {
		exp, kind = n.selectPlace(text, caret)
	}
	step := n.parseDecimal(text).step(exp, up)
	next := step.String()
	return n.commit(text, next, up, func() int {
		if n.float {
			return n.mapCaret(next, exp, kind)
		}
		// keep the distance from the right end, inside of the digits
		return min(max(len(next)-(len(text)-caret), n.signLen(next)), len(next))
	})
}

// commit applies a valid step and its caret position.
func (n *number[T]) commit(text, next string, up bool, where func() int) error {
	if !n.signed && strings.HasPrefix(next, "-") {
		return errNumberBelowZero
	}
	cand, err := n.convert(next)
	if err != nil {
		return err
	}
	// stepping toward the range from outside is allowed
	if (up && n.above(cand)) || (!up && n.below(cand)) {
		return n.boundsError()
	}
	if text != "" && n.float {
		// no silent jump by one ULP: repeated presses must not hide invisible changes
		cur, _ := n.convert(text) //nolint:errcheck // checked by the caller
		if cur == cand {
			return errNumberTooSmall
		}
	}
	at := where()
	n.prompt.applyInputChanged(inputChanged{Text: next, Cursor: &at})
	return nil
}

// signLen locates the first digit or decimal point after an optional sign.
func (n *number[T]) signLen(s string) int {
	if s != "" && (s[0] == '-' || s[0] == '+') {
		return 1
	}
	return 0
}

// decimalDraft represents coef × 10^-scale.
type decimalDraft struct {
	coef  *big.Int
	scale int
	// dot preserves a trailing decimal point.
	dot bool
}

// parseDecimal preserves decimal places for exact stepping of validated text.
func (n *number[T]) parseDecimal(s string) decimalDraft {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	whole, frac, dot := strings.Cut(s, ".")
	coef, _ := new(big.Int).SetString(whole+frac, 10)
	if neg {
		coef.Neg(coef)
	}
	return decimalDraft{coef: coef, scale: len(frac), dot: dot}
}

// pow10 supplies exact decimal scale factors.
func (d decimalDraft) pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// step adds or subtracts 10^exp exactly.
func (d decimalDraft) step(exp int, up bool) decimalDraft {
	scale := max(d.scale, -exp)
	coef := new(big.Int).Mul(d.coef, d.pow10(scale-d.scale))
	delta := d.pow10(scale + exp)
	if up {
		coef.Add(coef, delta)
	} else {
		coef.Sub(coef, delta)
	}
	return decimalDraft{coef: coef, scale: scale, dot: d.dot}
}

// String formats the value at its original scale.
func (d decimalDraft) String() string {
	digits := new(big.Int).Abs(d.coef).String()
	if len(digits) <= d.scale {
		digits = strings.Repeat("0", d.scale-len(digits)+1) + digits
	}
	whole, frac := digits[:len(digits)-d.scale], digits[len(digits)-d.scale:]
	s := whole
	if d.scale > 0 {
		s += "." + frac
	} else if d.dot {
		s += "."
	}
	if d.coef.Sign() < 0 {
		s = "-" + s
	}
	return s
}

type caretKind int

const (
	caretDigit caretKind = iota // before a digit, which selects its place
	caretEnd
	caretDot
	caretSign
)

// dotAt returns the decimal point's index or len(s).
func (n *number[T]) dotAt(s string) int {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return i
	}
	return len(s)
}

// selectPlace maps the caret to a decimal place.
func (n *number[T]) selectPlace(s string, caret int) (int, caretKind) {
	dot := n.dotAt(s)
	place := func(k int) int {
		if k < dot {
			return dot - k - 1
		}
		return dot - k
	}
	switch {
	case caret >= len(s):
		if dot == len(s)-1 {
			return -1, caretDigit // "12." creates tenths, with the caret before them
		}
		return place(len(s) - 1), caretEnd
	case s[caret] == '.':
		return 0, caretDot
	case caret < n.signLen(s):
		if first := n.signLen(s); first < len(s) && s[first] != '.' {
			return place(first), caretSign
		}
		return 0, caretSign
	}
	return place(caret), caretDigit
}

// mapCaret keeps the caret at the selected decimal place where possible.
func (n *number[T]) mapCaret(s string, exp int, kind caretKind) int {
	dot := n.dotAt(s)
	switch kind {
	case caretEnd:
		return len(s)
	case caretDot:
		return dot
	case caretSign:
		return 0
	case caretDigit:
	}
	if exp < 0 {
		return min(dot-exp, len(s))
	}
	return max(dot-exp-1, n.signLen(s))
}
