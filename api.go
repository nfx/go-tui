// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"io"
	"iter"
	"text/template"

	"github.com/nfx/go-tui/internal/choice"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/prettyjson"
	"github.com/nfx/go-tui/internal/progress"
	"github.com/nfx/go-tui/internal/prompt"
	"github.com/nfx/go-tui/internal/spinner"
	"github.com/nfx/go-tui/internal/subproc"
	"github.com/nfx/go-tui/internal/table"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"
)

var (
	ErrBug                 = errs.ErrBug
	ErrNoItems             = errs.ErrNoItems
	ErrInvalidState        = errs.ErrInvalidState
	ErrUnsupportedPlatform = errs.ErrUnsupportedPlatform
	ErrWrongWidget         = errs.ErrWrongWidget
	// ErrNoTTY is returned when a prompt needs a terminal and there is none.
	ErrNoTTY = errs.ErrNoTTY
	// ErrNoSpace is returned when the terminal is too small to draw the widget.
	ErrNoSpace = errs.ErrNoSpace
	// ErrEmptyLazyResult is empty lazy result.
	ErrEmptyLazyResult = errs.ErrEmptyLazyResult
	// ErrInvalidOption identifies invalid configuration values.
	ErrInvalidOption = errs.ErrInvalidOption
)

// Stderr returns the package-default append-only terminal writer.
//
// On interactive TTYs this routes writes through go-tui's shared terminal
// arbiter so prompts and external output are ordered by one renderer. On
// non-TTY outputs it returns the configured raw writer directly.
func Stderr() io.Writer {
	return term.Default.StderrWriter()
}

// DefaultTheme returns an editable copy of the installed theme, preserving helpers and unset colors.
func DefaultTheme() theme.Theme { return term.Default.CurrentTheme().Editable() }

// SetDefaultTheme validates and copies theme for operations started afterwards.
// On error, the installed theme is unchanged; running operations keep their snapshot.
// Funcs replaces existing helpers; start from [DefaultTheme] to retain them.
// Helper closures must be safe for concurrent use.
func SetDefaultTheme(th theme.Theme) error { return term.Default.InstallTheme(th) }

// WithFn registers or replaces a helper for operations started afterwards.
// Helpers must be safe for concurrent use and return a value or a value and an error.
// Invalid or reserved names and invalid signatures panic without changing the theme.
// Use [Theme.Funcs] with [SetDefaultTheme] to receive validation errors instead.
func WithFn(name string, fn any) {
	err := term.Default.UpdateTheme(func(t *theme.Theme) {
		if t.Funcs == nil {
			t.Funcs = template.FuncMap{}
		}
		t.Funcs[name] = fn
	})
	if err != nil {
		panic(fmt.Errorf("tui.WithFn: %w", err))
	}
}

// WithDefault supplies the answer of a prompt. Without a terminal the prompt
// returns it without reading input or printing; with a default missing, invalid
// or ambiguous it fails with [ErrInvalidOption], and without any default with
// [ErrNoTTY]. A default of nil, "", 0 or false is still a default.
//
// [Input] and [Password] pre-fill fmt.Sprint(d). [Number] pre-fills d in plain
// decimal notation. Choice widgets keep d as a typed value: the item for
// [Dropdown], the key for [DropdownKV] and [MultichoiceKVK], the value for
// [MultichoiceKVV]; a multichoice also takes a slice of them. Values are
// matched by equality, never by label, and must identify exactly one entry. A
// string that is not an item is a word-prefix hint on the labels.
//
// With a terminal, a dropdown treats a string as a match hint, not a pre-filled
// filter: items are ranked by similarity to d, and the dropdown returns early
// only when exactly one item matches d as a prefix. Multichoice checks a unique
// word-prefix match once and still requires Enter. Lazy choices resolve the
// default after successful exhaustion. [Confirm] takes a bool or "Yes"/"No".
func WithDefault(d any) option.Opt { return option.WithDefault(d) }

// WithNonEmpty rejects confirmed input containing only whitespace or no characters.
func WithNonEmpty() option.Opt { return prompt.WithNonEmpty() }

// WithValidation validates confirmed input in option order; the first error is shown
// below the input. fn receives the prompt context and blocks key handling while it
// runs. Errors must not expose the typed value, especially for [Password].
func WithValidation(fn func(context.Context, string) error) option.Opt {
	return prompt.WithValidation(fn)
}

// WithRegex rejects confirmed input that does not fully match pattern, showing message.
// A pattern that matches the empty string accepts it;
// combine with [WithNonEmpty] to require a value.
func WithRegex(pattern, message string) option.Opt { return prompt.WithRegex(pattern, message) }

func Input(ctx context.Context, label string, opts ...option.Opt) (string, error) {
	return prompt.Input(ctx, label, opts...)
}

func Password(ctx context.Context, label string, opts ...option.Opt) (string, error) {
	return prompt.Password(ctx, label, opts...)
}

type numberType = prompt.Numeric

// Number prompts for a decimal number of explicit type T (excluding uintptr).
// Invalid or out-of-range input stays editable. Errors return T's zero value.
//
// Up/Down step integers by one and floats at the caret's decimal place.
// Steps preserve fractional digits and reject overflow or invisible changes.
//
// Only [WithDefault], [WithAtLeast] and [WithAtMost] apply.
// Bounds are inclusive: typed values outside them are rejected on Enter, and
// Up/Down refuse to step past them.
func Number[T numberType](ctx context.Context, label string, opts ...option.Opt) (T, error) {
	return prompt.Number[T](ctx, label, opts...)
}

// WithAtLeast rejects [Number] values below v. The bound may have any numeric
// type; it is compared exactly and, for floats, at the precision of the answer.
func WithAtLeast[B numberType](v B) option.Opt { return prompt.WithAtLeast(v) }

// WithAtMost rejects [Number] values above v. See [WithAtLeast].
func WithAtMost[B numberType](v B) option.Opt { return prompt.WithAtMost(v) }

// Confirmf is [Confirm] with a formatted action.
func Confirmf(ctx context.Context, format string, a ...any) bool {
	return choice.Confirmf(ctx, format, a...)
}

// Confirm asks to choose Yes or No and reports whether Yes was chosen. No is a
// normal answer, retained like Yes; an error also returns false.
func Confirm(ctx context.Context, action string, opts ...option.Opt) bool {
	return choice.Confirm(ctx, action, opts...)
}

func DropdownKV[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	opts ...option.Opt,
) (K, V, error) {
	return choice.DropdownKV[K, V](ctx, label, items, opts...)
}

func Dropdown[T any](ctx context.Context, label string, items []T, opts ...option.Opt) (T, error) {
	return choice.Dropdown[T](ctx, label, items, opts...)
}

func DropdownLazy[V any](ctx context.Context, label string, itemFn iter.Seq2[V, error], o ...option.Opt) (V, error) {
	return choice.DropdownLazy[V](ctx, label, itemFn, o...)
}

func DropdownIndex(ctx context.Context, label string, items []any, o ...option.Opt) (int, error) {
	return choice.DropdownIndex(ctx, label, items, o...)
}

// WithHide skips printing the answer of a dropdown or an input.
func WithHide() option.Opt {
	return option.WithHide()
}

func WithOneReturn() option.Opt {
	return choice.WithOneReturn()
}

// WithTemplate shares main content across choice rows and answers; details appear only on the active row.
func WithTemplate(main string, activeDetails ...string) option.Opt {
	return choice.WithTemplate(main, activeDetails...)
}

// Multichoice returns checked items in input order. Enter confirms without
// checking the focused item; a successful empty selection is a non-nil slice.
// Items are shallowly copied. Use one active prompt per shared input reader.
func Multichoice[T any](ctx context.Context, label string, items []T, o ...option.Opt) ([]T, error) {
	return choice.Multichoice[T](ctx, label, items, o...)
}

// MultichoiceKVK snapshots entries sorted by fmt.Sprint(key) and returns keys.
// Equal formatted keys have unspecified relative order. Templates receive
// entries with Key and Value, including every element of the answer collection.
func MultichoiceKVK[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	o ...option.Opt,
) ([]K, error) {
	return choice.MultichoiceKVK[K, V](ctx, label, items, o...)
}

// MultichoiceKVV returns values in the same entry order as MultichoiceKVK.
// Duplicate and non-comparable values remain independently selectable.
func MultichoiceKVV[K comparable, V any](
	ctx context.Context,
	label string,
	items map[K]V,
	o ...option.Opt,
) ([]V, error) {
	return choice.MultichoiceKVV[K, V](ctx, label, items, o...)
}

// MultichoiceLazy displays a finite sequence progressively and enables Enter
// only after successful exhaustion. Results keep producer arrival order.
// Publication stops on cancellation; iterator work blocked outside yield must
// observe a context supplied by its caller to be interruptible.
func MultichoiceLazy[T any](
	ctx context.Context,
	label string,
	items iter.Seq2[T, error],
	o ...option.Opt,
) ([]T, error) {
	return choice.MultichoiceLazy[T](ctx, label, items, o...)
}

func WithExtensions(exts ...string) option.Opt {
	return choice.WithExtensions(exts...)
}

func WithStartDir(dir string) option.Opt {
	return choice.WithStartDir(dir)
}

func WithIgnoreUp() option.Opt {
	return choice.WithIgnoreUp()
}

func WithIgnoreDirs() option.Opt {
	return choice.WithIgnoreDirs()
}

func WithShowHidden() option.Opt {
	return choice.WithShowHidden()
}

func FilePicker(ctx context.Context, title string, o ...option.Opt) (string, error) {
	return choice.FilePicker(ctx, title, o...)
}

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

func WithFormatRate(f func(float64) string) option.Opt {
	return progress.WithFormatRate(f)
}

// WithWorkers configures the amount of parallel workers for NewParallelProgressBar.
func WithWorkers(workers int) option.Opt {
	return progress.WithWorkers(workers)
}

// NewMaxProgressBar returns progress bar towards the max number.
func NewMaxProgressBar(ctx context.Context, label string, size int64, opts ...option.Opt) (*Progressbar, error) {
	return progress.NewMaxProgressBar(ctx, label, size, opts...)
}

// NewSliceProgressBar returns progress bar that updates as long as iterator consumed.
func NewSliceProgressBar[T any](ctx context.Context, label string, slice []T, opts ...option.Opt) iter.Seq2[T, error] {
	return progress.NewSliceProgressBar[T](ctx, label, slice, opts...)
}

// NewParallelProgressBar executes yield in parallel while updating progress.
func NewParallelProgressBar[T any](
	ctx context.Context,
	label string,
	slice []T,
	yield func(T) error,
	opts ...option.Opt,
) error {
	return progress.NewParallelProgressBar[T](ctx, label, slice, yield, opts...)
}

func NewFileProgressReader(ctx context.Context, r io.Reader, label string, o ...option.Opt) (*wrapReader, error) {
	return progress.NewFileProgressReader(ctx, r, label, o...)
}

// Progressbar shows the progress of an operation in the terminal.
type Progressbar = progress.Progressbar

type wrapReader = progress.WrapReader

// LongRunning shows a spinner with the provided message while cb executes.
func LongRunning(ctx context.Context, message string, cb func(context.Context) error) error {
	return spinner.LongRunning(ctx, message, cb)
}

func NewSpinners(ctx context.Context, opt ...option.Opt) (*Spinners, error) {
	return spinner.NewSpinners(ctx, opt...)
}

// WithPrefixf sets the text shown next to a spinner added with [Spinners.Add].
func WithPrefixf(prefix string, args ...any) option.Opt {
	return spinner.WithPrefixf(prefix, args...)
}

// WithKeep will keep the spinner displayed after it's done.
func WithKeep() option.Opt {
	return spinner.WithKeep()
}

// Spinners shows a group of spinners that share a terminal region.
type Spinners = spinner.Spinners

// Spinner is one line of a [Spinners] group.
type Spinner = spinner.Spinner

// PrettyJSON pretty-prints JSON data, with two-space indentation unless the
// theme sets another [theme.JSON]. When the destination writer accepts color,
// keys use the accent color, dimmed with nesting depth, values the
// foreground and brackets a muted shade. Color follows the writer and the
// NO_COLOR, FORCE_COLOR and CLICOLOR_FORCE conventions, never os.Stdout, and
// the terminal is never queried. Without color the output is plain JSON.
// Malformed input returns an error before anything is written.
func PrettyJSON(w io.Writer, src any) error {
	return prettyjson.PrettyJSON(w, src)
}

// Browserf opens the specified URL in the default browser. addr is used as-is when
// called with no args; otherwise it's a fmt template whose string args get URL-escaped
// before substitution. A pre-built URL (already percent-encoded) must be passed with
// no args, since running it through Sprintf as a template would misparse its "%XX"
// sequences as format verbs.
func Browserf(addr string, args ...any) error {
	return subproc.Browserf(addr, args...)
}

// ShouldPasteFromClipboard returns the clipboard text, or "" when it cannot be read.
func ShouldPasteFromClipboard() string {
	return subproc.ShouldPasteFromClipboard()
}
