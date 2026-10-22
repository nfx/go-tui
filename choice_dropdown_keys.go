// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"
)

// hintMatches looks up prefix hints in the labels of items without drawing them.
func (d *dropdown) hintMatches(items []any) func(string) ([]int, error) {
	return func(hint string) ([]int, error) {
		t := newTrie()
		for i, item := range items {
			label, err := d.searchText(item)
			if err != nil {
				return nil, err
			}
			t.Add(label, i)
		}
		return hintFromTrie(t)(hint)
	}
}

// searchText returns searchable item content without theme decoration.
func (d *dropdown) searchText(item any) (text, error) {
	err := d.bindOnce()
	if err != nil {
		return nil, err
	}
	return d.projectContent(d.slot.value, item)
}

// setItem renders and indexes item at i.
func (d *dropdown) setItem(i int, item any) error {
	search, err := d.renderStates(i, item)
	if err != nil {
		return err
	}
	d.trie.Add(search, i)
	d.activeWidths[i] = d.active[i].width()
	d.widths[i] = d.inactive[i].width()
	d.longest = max(d.longest, d.widths[i], d.activeWidths[i])
	d.emit(dropdownAppendItem{
		Item:  item,
		Index: i,
		Text:  d.inactive[i].String(),
	})
	return nil
}

func (d *dropdown) loadItem(io *termIO, frame *bytes.Buffer, it itPair, more bool, space int) error {
	if !more {
		d.iterDone = true
		d.itItems = nil
		return nil
	}
	if it.err != nil {
		errs := []error{it.err}
		err := io.clear(space, frame)
		if err != nil {
			errs = append(errs, fmt.Errorf("clear: %w", err))
		}
		_, err = frame.WriteTo(io)
		if err != nil {
			errs = append(errs, fmt.Errorf("write: %w", err))
		}
		return errors.Join(errs...)
	}
	err := d.addItem(io.Height, it.item)
	if err != nil {
		return err
	}
	var errs []error
	err = io.clear(space, frame)
	if err != nil {
		errs = append(errs, fmt.Errorf("clear: %w", err))
	}
	_, err = frame.WriteTo(io)
	if err != nil {
		errs = append(errs, fmt.Errorf("write: %w", err))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// addItem appends an item and refreshes derived state. Until the user moves
// the selection, it stays on the first row, which is the best-ranked item.
func (d *dropdown) addItem(height int, item any) error {
	var current = -1 // original index of the item the user moved to
	if pos := d.offset + d.selected; d.moved && pos >= 0 && pos < len(d.relevant) {
		current = d.relevant[pos]
	}
	d.Items = append(d.Items, item)
	d.active = append(d.active, nil)
	d.activeWidths = append(d.activeWidths, 0)
	d.inactive = append(d.inactive, nil)
	d.widths = append(d.widths, 0)
	d.content = append(d.content, nil)
	d.search = append(d.search, "")
	err := d.setItem(len(d.Items)-1, item)
	if err != nil {
		return fmt.Errorf("set item: %w", err)
	}
	if d.seedLazy && !d.seeded {
		matches, err := d.target.matches(d.def.raw, []any{item})
		d.seeded = err == nil && len(matches) == 1
		d.seedIdx = len(d.Items) - 1
	}
	d.relevant = d.matching(string(d.typed))
	capacity := max(1, height/2)
	if pos := slices.Index(d.relevant, current); current >= 0 && pos >= 0 {
		// ranking may reorder rows, keep the user on the same item
		d.offset = min(d.offset, pos)
		if pos-d.offset >= capacity {
			d.offset = pos - capacity + 1
		}
		d.selected = pos - d.offset
	}
	d.displayed = d.relevant[min(d.offset, len(d.relevant)):]
	d.displayed = d.displayed[:min(len(d.displayed), capacity)]
	return nil
}

// matching returns relevant item indexes for the prefix, ranked by
// the [WithDefault] hint when there is one.
func (d *dropdown) matching(prefix string) []int {
	relevant := d.trie.Prefix(prefix)
	if d.rankHint != "" {
		d.rankByLevenstein(relevant, d.rankHint)
	}
	d.seedFirst(relevant)
	return relevant
}

// pressKeyRune updates dropdown state for an already-read key.
func (d *dropdown) pressKeyRune(io *termIO, key rune, displayed, space int) int {
	switch key {
	case '↑':
		d.pressUp(displayed)
		return -1
	case '↓':
		d.pressDown(displayed)
		return -1
	case keyEscape, keyIgnored:
		return -1
	}
	ev := d.decodeInputEvent(key)
	return d.applyInputEvent(io, ev, displayed, space)
}

func (d *dropdown) applyInputEvent(io *termIO, ev dropdownInputEvent, displayed, space int) int {
	prev := d.snapshotState()
	switch typed := ev.(type) {
	case dropdownInputConfirmed:
		if typed.Index < 0 || typed.Index >= len(d.Items) {
			return -1
		}
		pos := -1
		for i, idx := range d.relevant {
			if idx == typed.Index {
				pos = i
				break
			}
		}
		if pos < 0 {
			return -1
		}
		d.emit(dropdownConfirmed{Selected: typed.Index})
		return pos
	case dropdownFilteredWith:
		done := d.filterWith(typed.Prefix, displayed, space, io.Height/2)
		if done {
			d.emitStateChanges(prev)
			d.emit(dropdownConfirmed{Selected: d.relevant[0]})
			return 0
		}
	default:
		d.emitStateChanges(prev)
		return -1
	}
	d.emitStateChanges(prev)
	return -1
}

// pressKeyFromInput reads the next event from the pre-supplied input channel and applies it.
func (d *dropdown) pressKeyFromInput(tio *termIO, frame *bytes.Buffer, space, displayed int) (int, error) {
	var ev dropdownInputEvent
	select {
	case e, ok := <-d.input:
		if !ok {
			return -1, io.EOF
		}
		ev = e
	case <-tio.onResize:
		return -1, tio.clear(space, frame)
	}
	err := tio.clear(space, frame)
	if err != nil {
		return -1, err
	}
	i := d.applyInputEvent(tio, ev, displayed, space)
	if i >= 0 {
		// the caller clears the frame once; a second clear would start at
		// its top and erase the rows above it
		frame.Reset()
		return i, nil
	}
	return -1, nil
}

// pressKey applies the next key, or clears the frame on resize so the caller
// redraws with the new geometry.
func (d *dropdown) pressKey(
	ctx context.Context,
	tio *termIO,
	frame *bytes.Buffer,
	space, displayed int,
) (i int, err error) {
	if d.input != nil {
		return d.pressKeyFromInput(tio, frame, space, displayed)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	keys := tio.readEvents(ctx, nil)
	var ev keyEvent
	select {
	case e, ok := <-keys:
		tio.awaitReader()
		if !ok {
			return -1, d.Ctx.Err()
		}
		ev = e
	case <-tio.onResize:
		cancel()
		// an unreceived key stays with the input for the next read
		tio.awaitReader()
		return -1, tio.clear(space, frame)
	}
	if ev.err != nil {
		return -1, ev.err
	}
	err = tio.clear(space, frame)
	if err != nil {
		return -1, err
	}
	i = d.pressKeyRune(tio, ev.key, displayed, space)
	if i >= 0 {
		// the caller clears the frame once, see pressKeyFromInput
		frame.Reset()
		return i, nil
	}
	return -1, nil
}

// clearFrame removes the last render without drawing a new frame.
func (d *dropdown) clearFrame(io *termIO, frame *bytes.Buffer, space int) error {
	if space < 1 {
		return nil
	}
	if io.vp != nil && io.vp.fixedHeight {
		err := io.clear(space, io)
		if err != nil {
			return fmt.Errorf("clear: %w", err)
		}
		return nil
	}
	err := io.clear(space, frame)
	if err != nil {
		return fmt.Errorf("clear: %w", err)
	}
	_, err = frame.WriteTo(io)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (d *dropdown) pressUp(displayed int) {
	d.moved = true
	if d.offset > 0 && d.selected == 0 { // page up
		d.offset--
		d.displayed = d.relevant[d.offset : d.offset+displayed]
	} else if d.selected > 0 {
		d.selected--
	}
}

func (d *dropdown) pressDown(displayed int) {
	d.moved = true
	if d.offset+displayed < len(d.relevant) { // page down
		d.offset++
		d.displayed = d.relevant[d.offset : d.offset+displayed]
	} else if d.selected < displayed-1 {
		d.selected++
	}
}

func (d *dropdown) pressBackspace(io *termIO) {
	if len(d.typed) == 0 {
		return
	}
	d.typed = d.typed[:len(d.typed)-1]
	d.relevant = d.matching(string(d.typed))
	d.displayed = d.relevant[:min(len(d.relevant), io.Height/2)]
	d.selected = 0
	d.offset = 0
	d.moved = false
}

// filterWith replaces the current filter text and refreshes matching rows.
func (d *dropdown) filterWith(text string, displayed, space, height int) bool {
	prevTyped := string(d.typed)
	prevRelevant := d.relevant
	d.typed = []rune(text)
	d.relevant = d.matching(text)
	if d.OneReturn && len(d.relevant) == 1 {
		return true
	}
	if len(d.relevant) == 0 {
		d.typed = []rune(prevTyped)
		d.relevant = prevRelevant
		return false
	}
	limit := min(len(d.relevant), displayed, space)
	if utf8.RuneCountInString(text) < utf8.RuneCountInString(prevTyped) {
		limit = min(len(d.relevant), height)
	}
	d.displayed = d.relevant[:limit]
	d.selected = 0
	d.offset = 0
	d.moved = false
	return false
}

func (d *dropdown) pressAny(key rune, displayed, space int) bool {
	d.typed = append(d.typed, key)
	d.relevant = d.matching(string(d.typed))
	if d.OneReturn && len(d.relevant) == 1 {
		return true
	}
	if len(d.relevant) == 0 {
		d.typed = d.typed[:len(d.typed)-1]
		d.relevant = d.matching(string(d.typed))
		return false
	}
	d.displayed = d.relevant[:min(len(d.relevant), displayed, space)]
	d.selected = 0
	d.offset = 0
	d.moved = false
	return false
}
