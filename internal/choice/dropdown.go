// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/internal/trie"
)

type dropdown struct {
	render.ChoiceLabels
	Ctx          context.Context
	Label        string
	Items        []any
	trie         *trie.Trie
	active       []ansi.Text
	activeWidths []int
	inactive     []ansi.Text
	widths       []int
	relevant     []int
	displayed    []int
	Default      any
	Hide         bool
	OneReturn    bool

	projection

	// theme is the snapshot captured at construction. The dropdown renders
	// through it: family are the slots of the widget that
	// runs it, look is its binding to the destination, slot the bound
	// slots, label the row rendered once per run and hint the bound slot
	// rendered every frame with the count of hidden rows. A wrapper that
	// runs several dropdowns hands the binding of an earlier one on as
	// inherit, so that one invocation keeps one appearance.
	theme   *render.Compiled
	family  render.ChoiceFamily
	look    render.Binding
	inherit render.Binding
	slot    choiceSlots
	label   ansi.Text
	hint    render.BoundSlot
	// content is the projected main content of every item, and search the
	// plain text of it that filtering and ranking see.
	content []ansi.Text
	search  []string
	// rows is the height of the last frame.
	rows int

	ItemsFn func(prefix string) []any

	IterBatchSize int
	IterFn        func(any, error) bool
	iterDone      bool
	itItems       chan itPair

	selected int
	offset   int
	// moved is set once the user moves the selection with arrow keys, and
	// cleared when filtering starts over at the top of the list.
	moved    bool
	typed    []rune
	oneMatch string // see [WithDefault]
	// def is the raw default; target types it; normalize adapts it first.
	def       option.DefaultValue
	target    choiceTarget
	normalize func(any) any
	// seedIdx is the entry a typed default selects when seeded is set.
	seedIdx  int
	seeded   bool
	seedLazy bool
	// rankHint keeps the [WithDefault] value for ranking every candidate set,
	// including items that are added lazily, after oneMatch is consumed.
	rankHint string
	// longest is the widest active or inactive item seen so far.
	longest int

	in  io.Reader
	out io.Writer

	// TODO: special case for testing?..
	makeTermIO func(in io.Reader, out io.Writer) (*term.IO, error)
	eventSink  func(dropdownOutputEvent)
	input      <-chan dropdownInputEvent
}

// pick runs a configured dropdown over items and shows the answer.
func (d *dropdown) pick(ctx context.Context, label string, items []any) (int, error) {
	d.Label = label
	d.Items = items
	i, err := d.dropdownIndex(ctx)
	if err != nil {
		return -1, err
	}
	if !term.OutputIsTerminal(d.out) {
		return i, nil // an automatic answer prints nothing
	}
	err = d.themedAnswer(label, i)
	if err != nil {
		return -1, fmt.Errorf("answer: %w", err)
	}
	return i, nil
}

func (d *dropdown) dropdownIndex(ctx context.Context) (int, error) {
	// we must always parse templates, because we may still
	// want to show the answer in the call after this one.
	err := d.parseTemplates()
	if err != nil {
		return -1, fmt.Errorf("templates: %w", err)
	}
	return d.selectIndex(ctx)
}

// selectIndex runs a dropdown with parsed templates and returns the original
// item index. [dropdown.run] returns a position in d.relevant instead.
func (d *dropdown) selectIndex(ctx context.Context) (int, error) {
	if !term.OutputIsTerminal(d.out) {
		err := ctx.Err()
		if err != nil {
			return -1, err
		}
		i, err := d.resolveAutomatic(d.Items)
		if err != nil {
			return -1, err
		}
		return i, ctx.Err()
	}
	if d.OneReturn && len(d.Items) == 1 {
		return 0, nil
	}
	err := d.seedDefault()
	if err != nil {
		return -1, err
	}
	j, err := d.run(ctx)
	if err != nil {
		return -1, err
	}
	return d.relevant[j], nil
}

// resolveAutomatic picks the entry that answers without a terminal. The
// single-item shortcut of [WithOneReturn] wins over a default.
func (d *dropdown) resolveAutomatic(items []any) (int, error) {
	if d.OneReturn && len(items) == 1 {
		return 0, nil
	}
	if !d.def.Set {
		return -1, option.NoDefault(d.Label)
	}
	return d.target.resolveSingle(d.def.Raw, items, d.hintMatches(items))
}

// seedDefault makes a typed default the first and selected row of an
// interactive dropdown. String hints keep their own ranking.
func (d *dropdown) seedDefault() error {
	if !d.def.Set {
		return nil
	}
	if _, isHint := d.def.Raw.(string); isHint {
		return nil
	}
	if !d.target.accepts(d.def.Raw) {
		return option.BadDefault("%T has the wrong type", d.def.Raw)
	}
	if d.itItems != nil {
		d.seedLazy = true // the entry may arrive later
		return nil
	}
	matches, err := d.target.matches(d.def.Raw, d.Items)
	if err != nil {
		return err
	}
	i, err := uniqueMatch(matches, fmt.Sprintf("%T value", d.def.Raw))
	if err != nil {
		return err
	}
	d.seedIdx, d.seeded = i, true
	return nil
}

// seedFirst moves the seeded entry to the front of relevant.
func (d *dropdown) seedFirst(relevant []int) {
	if !d.seeded {
		return
	}
	pos := slices.Index(relevant, d.seedIdx)
	if pos > 0 {
		copy(relevant[1:pos+1], relevant[:pos])
		relevant[0] = d.seedIdx
	}
}

func newDropdown(ctx context.Context) *dropdown {
	in, out := term.DefaultStreams(ctx)
	theme := term.Default.CurrentTheme()
	return &dropdown{
		in:            in,
		out:           out,
		Ctx:           ctx,
		Label:         "Select from list",
		makeTermIO:    term.MakePromptIO,
		IterBatchSize: 10,
		trie:          trie.NewTrie(),
		theme:         theme,
		family:        theme.Templates.Dropdown,
	}
}

// implement [withPrompt] interface.
func (d *dropdown) SetHide() {
	d.Hide = true
}

// implement [withPrompt] interface.
func (d *dropdown) SetDefault(v any) {
	if d.normalize != nil {
		v = d.normalize(v)
	}
	d.def = option.DefaultValue{Set: true, Raw: v}
	hint, _ := v.(string) //nolint:errcheck // not a string is no hint
	d.oneMatch = hint
	d.rankHint = hint
}

// implements [withIO].
func (d *dropdown) SetReader(r io.Reader) {
	d.in = r
}

// implements [withIO].
func (d *dropdown) SetWriter(w io.Writer) {
	tui, ok := w.(*term.Tui)
	if ok {
		w = tui.PrependView(10) // TODO: height is properly available only after render, right?...
	}
	d.out = w
}

func (d *dropdown) height() int {
	return d.rows
}

// snapshotState captures dropdown state so key handlers can emit semantic diffs.
func (d *dropdown) snapshotState() dropdownState {
	// ranking may reorder relevant, while Diff needs numerically sorted indexes
	relevant := append([]int(nil), d.relevant...)
	slices.Sort(relevant)
	return dropdownState{
		prefix:   string(d.typed),
		relevant: relevant,
	}
}

// emitStateChanges publishes filter changes after a state transition.
func (d *dropdown) emitStateChanges(prev dropdownState) {
	curr := d.snapshotState()
	if curr.Equal(prev) {
		return
	}
	added, removed := curr.Diff(prev)
	d.emit(dropdownFilterChanged{
		Prefix:   curr.prefix,
		Matching: len(curr.relevant),
		Added:    added,
		Removed:  removed,
	})
}

// emit sends a typed dropdown output event to the optional sink.
func (d *dropdown) emit(ev dropdownOutputEvent) {
	if d.eventSink == nil {
		return
	}
	d.eventSink(ev)
}

// decodeInputEvent maps terminal keys into semantic dropdown input actions.
func (d *dropdown) decodeInputEvent(key rune) dropdownInputEvent {
	switch key {
	case term.KeyEnter:
		i := -1
		pos := d.offset + d.selected
		if pos >= 0 && pos < len(d.relevant) {
			i = d.relevant[pos]
		}
		return dropdownInputConfirmed{Index: i}
	case 0x7f: // backspace
		next := []rune(string(d.typed))
		if len(next) > 0 {
			next = next[:len(next)-1]
		}
		return dropdownFilteredWith{Prefix: string(next)}
	default:
		return dropdownFilteredWith{Prefix: string(d.typed) + string(key)}
	}
}

// run returns the position of the chosen item in d.relevant, not its original index.
func (d *dropdown) run(ctx context.Context) (int, error) {
	io, err := d.makeTermIO(d.in, d.out)
	if err != nil {
		return -1, fmt.Errorf("raw term: %w", err)
	}
	defer io.Restore() //nolint:errcheck // we can't do much about it here
	if io.Height < 3 {
		return -1, errs.ErrNoSpace
	}
	// before the event reader starts, as binding may probe the background
	err = d.bind(d.destination(ctx, io))
	if err == nil {
		err = d.renderChrome()
	}
	if err != nil {
		return -1, err
	}
	frame := bytes.NewBuffer(make([]byte, d.height()*io.Width))
	frame.Reset()
	if d.itItems != nil {
		return d.runLazy(ctx, io, frame)
	}
	for {
		i, err := d.runRender(ctx, io, frame)
		if err != nil {
			return -1, err
		}
		if i >= 0 {
			return i, nil
		}
	}
}

func (d *dropdown) runRender(ctx context.Context, io *term.IO, frame *bytes.Buffer) (int, error) {
	err := d.render(io, frame)
	if err != nil {
		var oneMatch oneHatch
		ok := errors.As(err, &oneMatch)
		if ok {
			return int(oneMatch), nil
		}
		return -1, fmt.Errorf("render: %w", err)
	}
	_, err = frame.WriteTo(io)
	if err != nil {
		return -1, fmt.Errorf("write: %w", err)
	}
	space := d.height()
	displayed := len(d.displayed)
	select {
	case it, more := <-d.itItems:
		err := d.loadItem(io, frame, it, more, space)
		return -1, err
	case <-d.Ctx.Done():
		err = io.Clear(space, frame)
		if err != nil {
			return -1, fmt.Errorf("clear: %w", err)
		}
		_, err = frame.WriteTo(io)
		if err != nil {
			return -1, fmt.Errorf("write: %w", err)
		}
		return -1, d.Ctx.Err()
	default:
		return d.runMain(ctx, io, frame, space, displayed)
	}
}

func (d *dropdown) runMain(ctx context.Context, io *term.IO, frame *bytes.Buffer, space, displayed int) (int, error) {
	i, err := d.pressKey(ctx, io, frame, space, displayed)
	if term.IsPasteText(err) {
		// Ctrl+V or CMD+V pressed
		return -1, nil
	} else if err != nil {
		frame.WriteTo(io) //nolint:errcheck // we can't do much about it here
		return -1, err
	}
	if i < 0 {
		return -1, nil
	}
	err = d.clearFrame(io, frame, space)
	if err != nil {
		return -1, err
	}
	return i, nil
}
