// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/internal/trie"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

type dropdownHeuristicLabelItem struct {
	ID   int
	Name string
}

type dropdownAnnotatedLabelItem struct {
	Name        string
	Description string `header:"label"`
}

type dropdownStringerLabelItem struct {
	ID int
}

func (d dropdownStringerLabelItem) String() string {
	return fmt.Sprintf("stringer-%d", d.ID)
}

type dropdownPointerStringerLabelItem struct {
	ID int
}

func (d *dropdownPointerStringerLabelItem) String() string {
	return fmt.Sprintf("ptr-stringer-%d", d.ID)
}

type dropdownDisplayNameLabelItem struct {
	DisplayName string
}

type dropdownFullNameLabelItem struct {
	FullName string
}

type dropdownSummaryLabelItem struct {
	Summary string
}

type dropdownTextLabelItem struct {
	Text string
}

type dropdownSubjectLabelItem struct {
	Subject string
}

// testIOforDropdown provides keys and frames for a dropdown at a fixed terminal size.
func testIOforDropdown(t *testing.T, width, height int, o ...option.Opt) (*terst.Keys, *terst.Output, option.Opt) {
	t.Helper()
	terst.UseASCII(t)
	keys, out := terst.UseFrames(t)
	return keys, out, option.WithOptions(append(option.Opts{
		option.OpT(func(d *dropdown) error {
			d.makeTermIO = terst.FixedTermIO(width, height)
			return nil
		}),
	}, o...,
	)...)
}

func confirmForTest(t *testing.T) (in *terst.Keys, out *terst.Output, result chan bool) {
	t.Helper()
	in, out, opts := testIOforDropdown(t, 80, 120)
	result = make(chan bool)
	go func() {
		defer close(result)
		result <- Confirm(t.Context(), "Are you sure?", opts)
	}()
	return in, out, result
}

func TestSimpleCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t, "\r? Are you sure?\n\r| > Yes\n\r|   No\n\r| ^v move · type filter\n\r", out.Next())
	in.Send("\x0d") // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", out.Next())
	assert.Equal(t, "+ Are you sure? · Yes\n", out.AwaitOutput("+ Are you sure? · Yes\n"))
	assert.Equal(t, true, <-res)
}

func TestDenyCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t, "\r? Are you sure?\n\r| > Yes\n\r|   No\n\r| ^v move · type filter\n\r", out.Next())
	in.Send("\x1b\x5b\x42") // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Are you sure?\n\r|   Yes\n\r| > No\n\r| ^v move · type filter\n\r",
		out.Next(),
	)
	in.Send("\x0d") // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", out.Next())
	assert.Equal(t, "+ Are you sure? · No\n", out.AwaitOutput("+ Are you sure? · No\n"))
	assert.Equal(t, false, <-res)
}

func TestDownAndUpCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t, "\r? Are you sure?\n\r| > Yes\n\r|   No\n\r| ^v move · type filter\n\r", out.Next())
	in.Send("\x1b\x5b\x42") // down
	out.Next()              // frame render
	in.Send("\x1b\x5b\x41") // up
	out.Next()              // frame render
	in.Send("\x0d")         // enter
	out.Next()              // clear
	assert.Equal(t, "+ Are you sure? · Yes\n", out.AwaitOutput("+ Are you sure? · Yes\n"))
	assert.Equal(t, true, <-res)
}

// picked is the outcome of a dropdown run in the background. The goroutine
// only sends it, so that a test failing early cannot make it report after
// the test has ended.
type picked struct {
	value string
	err   error
}

// assertPicked checks that the dropdown behind res chose want.
func assertPicked(t *testing.T, want string, res <-chan picked) {
	t.Helper()
	r := <-res
	assert.NoError(t, r.err)
	assert.Equal(t, want, r.value)
}

// overflowForTest starts a dropdown with more choices than visible rows.
func overflowForTest(t *testing.T) (in *terst.Keys, out *terst.Output, result chan picked) {
	t.Helper()
	// room for the label, two items and the count, with the row below the
	// frame free; the key hint shows once there is no count
	in, out, opts := testIOforDropdown(t, 30, 5)
	result = make(chan picked, 1)
	go func() {
		v, err := Dropdown(t.Context(), "Pick letter", []string{
			"A", "B", "C", "D", "E",
		}, opts)
		result <- picked{v, err}
	}()
	return in, out, result
}

func TestMoreItems(t *testing.T) {
	in, out, res := overflowForTest(t)
	assert.Equal(t, "\r? Pick letter\n\r| > A\n\r|   B\n\r| 3 more · ^v move · type fi…\n\r", out.Next())
	in.Send("\x1b\x5b\x42") // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > B\n\r|   C\n\r| 2 more · ^v move · type fi…\n\r",
		out.Next(),
	)
	in.Send("\x1b\x5b\x42") // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > C\n\r|   D\n\r| 1 more · ^v move · type fi…\n\r",
		out.Next(),
	)
	in.Send("\x1b\x5b\x42") // down
	// the key hint takes the row of the count
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > D\n\r|   E\n\r| ^v move · type filter\n\r",
		out.Next(),
	)
	in.Send("\x1b\x5b\x42") // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r|   D\n\r| > E\n\r| ^v move · type filter\n\r",
		out.Next(),
	)
	in.Send("\x1b\x5b\x42") // down, no more items, might bell
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r|   D\n\r| > E\n\r| ^v move · type filter\n\r",
		out.Next(),
	)
	in.Send("\x1b\x5b\x41") // up
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > D\n\r|   E\n\r| ^v move · type filter\n\r",
		out.Next(),
	)
	in.Send("\x0d") // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", out.Next())
	assert.Equal(t, "+ Pick letter · D\n", out.AwaitOutput("+ Pick letter · D\n"))
	assertPicked(t, "D", res)
}

func TestMoreItemsUp(t *testing.T) {
	in, out, res := overflowForTest(t)
	assert.Equal(t, "\r? Pick letter\n\r| > A\n\r|   B\n\r| 3 more · ^v move · type fi…\n\r", out.Next())
	in.Send("\x1b\x5b\x42") // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > B\n\r|   C\n\r| 2 more · ^v move · type fi…\n\r",
		out.Next(),
	)
	in.Send("\x1b\x5b\x41") // up
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > A\n\r|   B\n\r| 3 more · ^v move · type fi…\n\r",
		out.Next(),
	)
	in.Send("\x0d") // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", out.Next())
	assert.Equal(t, "+ Pick letter · A\n", out.AwaitOutput("+ Pick letter · A\n"))
	assertPicked(t, "A", res)
}

// otherDropdownForTest starts a dropdown for filtering and clipping checks.
func otherDropdownForTest(t *testing.T) (in *terst.Keys, out *terst.Output, result chan picked) {
	t.Helper()
	// room for the label, two items and the count, as in overflowForTest
	in, out, opts := testIOforDropdown(t, 30, 5)
	result = make(chan picked, 1)
	go func() {
		v, err := Dropdown(t.Context(), "Neque porro", []string{
			"Lorem ipsum",
			"dolor sit amet",
			"adipiscing elit",
			"Quisque porttitor",
			"condimentum libero",
		}, opts)
		result <- picked{v, err}
	}()
	return in, out, result
}

func TestDropdownFiltering(t *testing.T) {
	in, out, res := otherDropdownForTest(t)
	assert.Equal(
		t,
		"\r? Neque porro\n\r| > Lorem ipsum\n\r|   dolor sit amet\n\r| 3 more · ^v move · type fi…\n\r",
		out.Next(),
	)
	in.Send("c")
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Neque porro\n\r| > condimentum libero\n\r| ^v move · type filter\n\r",
		out.Next(),
	)
	in.Send("\x7f") // backspace
	assert.Equal(
		t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r"+"\r? Neque porro\n\r| > Lorem ipsum\n\r|   dolor sit amet\n\r| 3 more · ^v move · type fi…\n\r",
		out.Next(),
	)
	in.Send("l")
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Neque porro\n\r| > Lorem ipsum\n\r|   condimentum libero\n\r| ^v move · type filter\n\r",
		out.Next(),
	)
	in.Send("\x1b\x5b\x42") // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Neque porro\n\r|   Lorem ipsum\n\r| > condimentum libero\n\r| ^v move · type filter\n\r",
		out.Next(),
	)
	in.Send("\x0d") // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", out.Next())
	assert.Equal(
		t,
		"+ Neque porro · condimentum libero\n",
		out.AwaitOutput("+ Neque porro · condimentum libero\n"),
	)
	assertPicked(t, "condimentum libero", res)
}

func dropdownKVForTest(t *testing.T, items map[string]int) (in *terst.Keys, out *terst.Output, result chan struct {
	key   string
	value int
	err   error
}) {
	t.Helper()
	in, out, opts := testIOforDropdown(t, 80, 120)
	result = make(chan struct {
		key   string
		value int
		err   error
	})
	go func() {
		defer close(result)
		key, value, err := DropdownKV(t.Context(), "Select item", items, opts)
		result <- struct {
			key   string
			value int
			err   error
		}{key, value, err}
	}()
	return in, out, result
}

func TestDropdownKVEmpty(t *testing.T) {
	_, _, res := dropdownKVForTest(t, map[string]int{})
	result := <-res
	assert.Equal(t, "", result.key)
	assert.Equal(t, 0, result.value)
	assert.Equal(t, errs.ErrNoItems, result.err)
}

func TestDropdownKVSingleItem(t *testing.T) {
	in, out, res := dropdownKVForTest(t, map[string]int{"apple": 42})
	assert.Equal(t, "\r? Select item\n\r| > apple\n\r| ^v move · type filter\n\r", out.Next())
	in.Send("\x0d") // enter
	out.Next()
	assert.Equal(t, "+ Select item · apple\n", out.AwaitOutput("+ Select item · apple\n"))
	result := <-res
	assert.Equal(t, "apple", result.key)
	assert.Equal(t, 42, result.value)
	assert.NoError(t, result.err)
}

func TestDropdownKVMultipleItems(t *testing.T) {
	in, out, res := dropdownKVForTest(t, map[string]int{"zebra": 1, "apple": 2, "banana": 3})
	assert.Equal(
		t,
		"\r? Select item\n\r| > apple\n\r|   banana\n\r|   zebra\n\r| ^v move · type filter\n\r",
		out.Next(),
	)
	in.Send("\x1b\x5b\x42") // down
	clear5 := "\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"
	assert.Equal(t,
		clear5+"\r? Select item\n\r|   apple\n\r| > banana\n\r|   zebra\n\r| ^v move · type filter\n\r",
		out.Next())
	in.Send("\x0d") // enter
	assert.Equal(t, clear5, out.Next())
	assert.Equal(t, "+ Select item · banana\n", out.AwaitOutput("+ Select item · banana\n"))
	result := <-res
	assert.Equal(t, "banana", result.key)
	assert.Equal(t, 3, result.value)
	assert.NoError(t, result.err)
}

func TestDropdownRunSelectsWithArrow(t *testing.T) {
	reader := terst.NewKeys(t, terst.KeyDown, terst.KeyEnter)
	d := newDropdown(t.Context())
	d.Items = []any{"one", "two"}
	d.in = reader
	d.out = &bytes.Buffer{}
	d.makeTermIO = terst.FixedTermIO(20, 6)
	idx, err := d.dropdownIndex(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownRunIgnoresSpecialKeys(t *testing.T) {
	reader := terst.NewKeys(t, "\x1b[3~", "\x1bx", terst.KeyEsc+terst.KeyDown, terst.KeyEnter)
	d := newDropdown(t.Context())
	d.Items = []any{"one", "two"}
	d.in = reader
	d.out = &bytes.Buffer{}
	d.makeTermIO = terst.FixedTermIO(20, 6)
	idx, err := d.dropdownIndex(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
	assert.Equal(t, "", string(d.typed))
}

func TestDropdownRunConsumesInputEvents(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"one", "two"}
	d.in = bytes.NewBuffer(nil)
	d.out = &bytes.Buffer{}
	input := make(chan dropdownInputEvent, 1)
	input <- dropdownInputConfirmed{Index: 1}
	close(input)
	d.input = input
	d.makeTermIO = terst.FixedTermIO(20, 6)
	idx, err := d.dropdownIndex(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownRunOneReturn(t *testing.T) {
	reader := terst.NewKeys(t, "b")
	d := newDropdown(t.Context())
	d.Items = []any{"alpha", "beta"}
	d.OneReturn = true
	d.in = reader
	d.out = &bytes.Buffer{}
	d.makeTermIO = terst.FixedTermIO(20, 6)
	idx, err := d.dropdownIndex(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownOptionHelpers(t *testing.T) {
	d := newDropdown(t.Context())

	for _, opt := range []option.Opt{
		WithOneReturn(),
		option.WithHide(),
		WithTemplate(".Label"),
		WithTemplate(".Label"),
	} {
		assert.NoError(t, opt(d))
	}
	assert.True(t, d.OneReturn)
	assert.True(t, d.Hide)
}

func TestShowAnswerWritesWhenVisible(t *testing.T) {
	d := newDropdown(t.Context())
	d.Label, d.Items = "Test", []any{"value"}
	out := &bytes.Buffer{}
	d.out = out
	bindDropdown(t, d)
	assert.NoError(t, d.themedAnswer("Test", 0))
	assert.Equal(t, "✔ Test · value\n", out.String())
}

func TestDropdownItemLabelUsesHeuristicField(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownHeuristicLabelItem{ID: 42, Name: "alpha"}
	assert.Equal(t, "alpha", d.ItemLabel(item))
}

func TestDropdownItemLabelUsesDisplayNameHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownDisplayNameLabelItem{DisplayName: "service-a"}
	assert.Equal(t, "service-a", d.ItemLabel(item))
}

func TestDropdownItemLabelUsesFullNameHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownFullNameLabelItem{FullName: "Example Service"}
	assert.Equal(t, "Example Service", d.ItemLabel(item))
}

func TestDropdownItemLabelUsesSummaryHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownSummaryLabelItem{Summary: "concise summary"}
	assert.Equal(t, "concise summary", d.ItemLabel(item))
}

func TestDropdownItemLabelUsesTextHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownTextLabelItem{Text: "plaintext value"}
	assert.Equal(t, "plaintext value", d.ItemLabel(item))
}

func TestDropdownItemLabelUsesSubjectHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownSubjectLabelItem{Subject: "subject line"}
	assert.Equal(t, "subject line", d.ItemLabel(item))
}

func TestDropdownItemLabelUsesAnnotation(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownAnnotatedLabelItem{
		Name:        "fallback",
		Description: "annotated",
	}
	assert.Equal(t, "annotated", d.ItemLabel(item))
}

func TestDropdownItemLabelFallsBackToStringer(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownStringerLabelItem{ID: 7}
	assert.Equal(t, "stringer-7", d.ItemLabel(item))
}

func TestDropdownItemLabelFallsBackToPointerStringer(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownPointerStringerLabelItem{ID: 11}
	assert.Equal(t, "ptr-stringer-11", d.ItemLabel(item))
}

func TestDropdownSetItemUsesResolvedLabelForTrie(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownHeuristicLabelItem{ID: 3, Name: "omega"}
	d.active = make([]ansi.Text, 1)
	d.activeWidths = make([]int, 1)
	d.inactive = make([]ansi.Text, 1)
	d.widths = make([]int, 1)
	d.content = make([]ansi.Text, 1)
	d.search = make([]string, 1)
	d.relevant = make([]int, 1)
	d.trie = trie.NewTrie()
	bindDropdown(t, d)
	assert.NoError(t, d.setItem(0, item))
	assert.Equal(t, []int{0}, d.trie.Prefix("ome"))
	assert.NotContains(t, d.inactive[0].String(), "{")
}

func TestDropdownShowAnswerUsesResolvedLabel(t *testing.T) {
	d := newDropdown(t.Context())
	out := &bytes.Buffer{}
	d.out = out
	d.Items = []any{dropdownHeuristicLabelItem{ID: 9, Name: "delta"}}
	bindDropdown(t, d)
	assert.NoError(t, d.themedAnswer("Pick", 0))
	assert.Contains(t, out.String(), "delta")
	assert.NotContains(t, out.String(), "{")
}

func TestDropdownParseTemplatesError(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithTemplate("{{")(d))
	assert.Error(t, d.parseTemplates())
}

func TestDropdownShowAnswerWriteError(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"Value"}
	bindDropdown(t, d)
	d.out = terst.FailingWriter{Err: io.EOF}
	assert.Error(t, d.themedAnswer("Label", 0))
}

func TestDropdownRunMainConsumesInputChannel(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = trie.NewTrie()
	d.trie.Add(ansi.Text("a"), 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	input := make(chan dropdownInputEvent, 1)
	input <- dropdownInputConfirmed{Index: 0}
	d.input = input
	io := terst.NewTermIO(10, 4)
	frame := bytes.NewBuffer(nil)
	i, err := d.runMain(d.Ctx, io, frame, 1, 1)
	assert.NoError(t, err)
	assert.Equal(t, 0, i)
}

func TestDropdownRunMainRejectsInvalidConfirmedIndex(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	input := make(chan dropdownInputEvent, 1)
	input <- dropdownInputConfirmed{Index: 5}
	d.input = input
	io := terst.NewTermIO(10, 4)
	frame := bytes.NewBuffer(nil)
	i, err := d.runMain(d.Ctx, io, frame, 1, 1)
	assert.NoError(t, err)
	assert.Equal(t, -1, i)
}

func TestDropdownAddItemPreservesFilter(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = trie.NewTrie()
	bindDropdown(t, d)
	assert.NoError(t, d.addItem(6, "alpha"))
	assert.True(t, !d.filterWith("a", len(d.displayed), 6, 3))
	assert.Equal(t, []int{0}, d.relevant)
	assert.NoError(t, d.addItem(6, "beta"))
	assert.Equal(t, []int{0}, d.relevant)
	assert.Equal(t, []int{0}, d.displayed)
}

func TestDropdownRunRenderLoadsItem(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"seed"}
	bindDropdown(t, d)
	tio := terst.NewTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	ch := make(chan itPair, 1)
	ch <- itPair{item: "next"}
	close(ch)
	d.itItems = ch
	_, err := d.runRender(d.Ctx, tio, frame)
	assert.NoError(t, err)
}

func TestDropdownLoadItemDone(t *testing.T) {
	d := newDropdown(t.Context())
	tio := terst.NewTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	d.itItems = make(chan itPair)
	err := d.loadItem(tio, frame, itPair{}, false, 1)
	assert.NoError(t, err)
	assert.True(t, d.itItems == nil)
	assert.True(t, d.iterDone)
}

func TestDropdownAddItemEmitsAppendEvent(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"seed"}
	bindDropdown(t, d)
	tio := terst.NewTermIO(20, 2)
	_, err := d.renderInit(tio)
	assert.NoError(t, err)
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	err = d.addItem(tio.Height, "next")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(events))
	appended, ok := events[0].(dropdownAppendItem)
	assert.True(t, ok)
	assert.Equal(t, "next", appended.Item)
	assert.Equal(t, 1, appended.Index)
	assert.Contains(t, appended.Text, "next")
}

func TestDropdownLoadItemError(t *testing.T) {
	d := newDropdown(t.Context())
	tio := terst.NewTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	err := d.loadItem(tio, frame, itPair{err: io.EOF}, true, 1)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownLoadItemWriteError(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"seed"}
	assert.NoError(t, d.parseTemplates())
	initIO := terst.NewTermIO(20, 6)
	_, err := d.renderInit(initIO)
	assert.NoError(t, err)
	tio := &term.IO{
		In:      bytes.NewBuffer(nil),
		Out:     terst.FailingWriter{Err: io.EOF},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	err = d.loadItem(tio, frame, itPair{item: "next"}, true, 1)
	assert.Error(t, err)
}

func TestDropdownRunRenderWriteError(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	bindDropdown(t, d)
	tio := &term.IO{
		In:      bytes.NewBuffer([]byte{term.KeyEnter}),
		Out:     terst.FailingWriter{Err: io.EOF},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runRender(d.Ctx, tio, frame)
	assert.Error(t, err)
}

func TestDropdownRunRenderUsesMain(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	bindDropdown(t, d)
	tio := &term.IO{
		In:      bytes.NewBuffer([]byte{term.KeyEnter}),
		Out:     &bytes.Buffer{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	i, err := d.runRender(d.Ctx, tio, frame)
	assert.NoError(t, err)
	assert.True(t, i >= 0)
}

func TestDropdownRunMainPasteIgnored(t *testing.T) {
	d := newDropdown(t.Context())
	tio := &term.IO{
		In:      bytes.NewBufferString("ab"),
		Out:     &bytes.Buffer{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runMain(d.Ctx, tio, frame, 1, 1)
	assert.NoError(t, err)
}

func TestDropdownRunMainReadError(t *testing.T) {
	d := newDropdown(t.Context())
	tio := &term.IO{
		In:      bytes.NewBuffer(nil),
		Out:     &bytes.Buffer{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runMain(d.Ctx, tio, frame, 1, 1)
	assert.Error(t, err)
}

func TestDropdownRunRenderContextDone(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	bindDropdown(t, d)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d.Ctx = ctx
	tio := &term.IO{
		In:      bytes.NewBuffer([]byte{term.KeyEnter}),
		Out:     &bytes.Buffer{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runRender(d.Ctx, tio, frame)
	assert.Error(t, err)
}

func TestDropdownAddItemDeduplicatesLazyRelevantIndexes(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, d.parseTemplates())
	tio := terst.NewTermIO(20, 6)
	_, err := d.renderInit(tio)
	assert.NoError(t, err)
	err = d.addItem(tio.Height, "alpha beta")
	assert.NoError(t, err)
	assert.Equal(t, []int{0}, d.relevant)
	assert.Equal(t, []int{0}, d.displayed)
}

// itemsLoadedWriter signals once the dropdown renders a frame with at least min items.
// Writes happen on the dropdown goroutine, so reading Items here is race-free.
type itemsLoadedWriter struct {
	io.Writer
	d     *dropdown
	ready chan struct{}
	once  *sync.Once
	min   int
}

func (w itemsLoadedWriter) Write(p []byte) (int, error) {
	if len(w.d.Items) >= w.min {
		w.once.Do(func() { close(w.ready) })
	}
	return w.Writer.Write(p)
}

// notifyItemsLoaded closes the channel once minItems are loaded and rendered.
func notifyItemsLoaded(minItems int) (option.Opt, <-chan struct{}) {
	ready := make(chan struct{})
	once := &sync.Once{}
	return option.OpT(func(d *dropdown) error {
		d.out = itemsLoadedWriter{Writer: d.out, d: d, ready: ready, once: once, min: minItems}
		return nil
	}), ready
}

func waitForItems(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	terst.Recv(t, ready)
}

func assertNoWaitersFor(t *testing.T, r *terst.Keys, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if r.Waiters() > 0 {
			t.Fatalf("unexpected blocked readers: %d", r.Waiters())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func fixedTermOpt() option.Opt {
	return option.OpT(func(d *dropdown) error {
		d.makeTermIO = terst.FixedTermIO(20, 6)
		return nil
	})
}

func TestDropdownIndexUniqueDefaultReturnsOriginalIndex(t *testing.T) {
	terst.UseTerminalInput(t, nil)
	i, err := DropdownIndex(t.Context(), "pick", []any{"xxx", "b", "a"},
		fixedTermOpt(), option.WithDefault("a"))
	assert.NoError(t, err)
	assert.Equal(t, 2, i)

	v, err := Dropdown(t.Context(), "pick", []string{"xxx", "b", "a"},
		fixedTermOpt(), option.WithDefault("a"))
	assert.NoError(t, err)
	assert.Equal(t, "a", v)
}

func TestDropdownFilterEventsIgnoreRankingOrder(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, option.WithDefault("ap")(d))
	bindDropdown(t, d)
	tio := terst.NewTermIO(20, 6)
	frame := &bytes.Buffer{}
	for _, item := range []string{"banana", "apricot", "apple"} {
		_, _, err := d.handleLazyItem(tio, frame, 0, itPair{item: item}, true)
		assert.NoError(t, err)
	}
	var events []dropdownFilterChanged
	d.eventSink = func(ev dropdownOutputEvent) {
		f, ok := ev.(dropdownFilterChanged)
		if ok {
			events = append(events, f)
		}
	}
	d.applyInputEvent(tio, dropdownFilteredWith{Prefix: "a"}, 3, 6)
	d.applyInputEvent(tio, dropdownFilteredWith{Prefix: "apr"}, 3, 6)
	assert.Equal(t, 2, len(events))
	assert.Equal(t, []int{0}, events[0].Removed)
	assert.Equal(t, []int(nil), events[0].Added)
	assert.Equal(t, []int{2}, events[1].Removed)
	assert.Equal(t, []int(nil), events[1].Added)
}

func TestDropdownEagerRedrawsOnResizeWhileIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		cancel()
		_ = reader.Close() //nolint:errcheck // test cleanup
		_ = writer.Close() //nolint:errcheck // test cleanup
	})
	out := terst.NewOutput(t)
	resize := make(chan struct{})
	resCh := make(chan string, 1)
	terst.UseIO(t, reader, out)
	go func() {
		value, _ := Dropdown(ctx, "Pick", []string{"a", "b"}, option.WithOptions( //nolint:errcheck // test cleanup
			option.OpT(func(d *dropdown) error {
				d.makeTermIO = func(in io.Reader, w io.Writer) (*term.IO, error) {
					return &term.IO{
						In: in, Out: w, Width: 20, Height: 6,
						Restore:  func() error { return nil },
						OnResize: resize,
					}, nil
				}
				return nil
			}),
		))
		resCh <- value
	}()
	out.Next()
	terst.Send(t, resize, struct{}{})
	out.Next()
	// the retained reader still delivers the next key
	_, err := writer.Write([]byte{term.KeyEnter})
	assert.NoError(t, err)
	v := terst.Recv(t, resCh)
	assert.Equal(t, "a", v)
}

func TestDropdownTypedDefaults(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()

	servers := []server{{"alpha", 1}, {"beta", 2}}
	got, err := Dropdown(ctx, "Pick", servers, option.WithDefault(server{"beta", 2}))
	assert.NoError(t, err)
	assert.Equal(t, servers[1], got)

	// identical labels, distinct values
	same := []tagged{{"x", 1}, {"x", 2}}
	gotTagged, err := Dropdown(ctx, "Pick", same, option.WithDefault(tagged{"x", 2}))
	assert.NoError(t, err)
	assert.Equal(t, 2, gotTagged.ID)

	// pointer identity
	a, b := &server{"same", 1}, &server{"same", 1}
	ptr, err := Dropdown(ctx, "Pick", []*server{a, b}, option.WithDefault(b))
	assert.NoError(t, err)
	assert.True(t, ptr == b)

	// non-comparable items
	slices, err := Dropdown(ctx, "Pick", [][]int{{1}, {2, 3}}, option.WithDefault([]int{2, 3}))
	assert.NoError(t, err)
	assert.Equal(t, []int{2, 3}, slices)
	maps, err := Dropdown(
		ctx,
		"Pick",
		[]map[string]int{{"a": 1}, {"b": 2}},
		option.WithDefault(map[string]int{"b": 2}),
	)
	assert.NoError(t, err)
	assert.Equal(t, map[string]int{"b": 2}, maps)

	idx, err := DropdownIndex(ctx, "Pick", []any{1, "two", 3.5}, option.WithDefault(3.5))
	assert.NoError(t, err)
	assert.Equal(t, 2, idx)

	// wrong type, missing value, duplicates
	_, err = Dropdown(ctx, "Pick", []int64{1, 2}, option.WithDefault(2))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", servers, option.WithDefault(server{"gamma", 3}))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []int{1, 2, 1}, option.WithDefault(1))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", servers, option.WithDefault(true))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	// nil
	var none *server
	nilPtr, err := Dropdown(ctx, "Pick", []*server{a, none}, option.WithDefault(nil))
	assert.NoError(t, err)
	assert.True(t, nilPtr == nil)
	_, err = Dropdown(ctx, "Pick", []*server{a}, option.WithDefault(nil))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []int{1}, option.WithDefault(nil))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []*server{none, none}, option.WithDefault(nil))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	// functions cannot be identified, NaN does not equal itself
	fn := func() {}
	_, err = Dropdown(ctx, "Pick", []func(){fn}, option.WithDefault(fn))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	nan := 0.0
	nan /= nan
	_, err = Dropdown(ctx, "Pick", []float64{nan}, option.WithDefault(nan))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	_, err = Dropdown(ctx, "Pick", servers)
	assert.ErrorIs(t, err, errs.ErrNoTTY)
}

func TestDropdownStringDefaults(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()

	got, err := Dropdown(ctx, "Pick", []string{"ab", "a"}, option.WithDefault("a"))
	assert.NoError(t, err)
	assert.Equal(t, "a", got)

	got, err = Dropdown(ctx, "Pick", []string{"alpha", "beta"}, option.WithDefault("be"))
	assert.NoError(t, err)
	assert.Equal(t, "beta", got)

	_, err = Dropdown(ctx, "Pick", []string{"alpha", "apple"}, option.WithDefault("a"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []string{"alpha", "apple"}, option.WithDefault("zz"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []string{"alpha", "apple"}, option.WithDefault(""))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	// an exact duplicate is ambiguous, whatever a hint could do
	_, err = Dropdown(ctx, "Pick", []string{"a", "a", "ab"}, option.WithDefault("a"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	got, err = Dropdown(ctx, "Pick", []string{"", "x"}, option.WithDefault(""))
	assert.NoError(t, err)
	assert.Equal(t, "", got)

	// a hint finds a struct by its label
	servers := []server{{"alpha", 1}, {"beta", 2}}
	s, err := Dropdown(ctx, "Pick", servers, option.WithDefault("bet"))
	assert.NoError(t, err)
	assert.Equal(t, servers[1], s)
}

func TestDropdownKVDefault(t *testing.T) {
	terst.Quiet(t)
	items := map[string]int{"one": 1, "two": 2}
	k, v, err := DropdownKV(t.Context(), "Pick", items, option.WithDefault("two"))
	assert.NoError(t, err)
	assert.Equal(t, "two", k)
	assert.Equal(t, 2, v)

	// the value is not a key lookup
	_, _, err = DropdownKV(t.Context(), "Pick", map[int]int{1: 2, 2: 1}, option.WithDefault("x"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	k2, v2, err := DropdownKV(t.Context(), "Pick", map[int]int{1: 2, 2: 1}, option.WithDefault(2))
	assert.NoError(t, err)
	assert.Equal(t, 2, k2)
	assert.Equal(t, 1, v2)
}

func TestDropdownOneReturn(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()
	got, err := Dropdown(ctx, "Pick", []string{"only"}, WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, "only", got)
	// the shortcut wins over a hint
	got, err = Dropdown(ctx, "Pick", []string{"only"}, WithOneReturn(), option.WithDefault("zzz"))
	assert.NoError(t, err)
	assert.Equal(t, "only", got)
	_, err = Dropdown(ctx, "Pick", []string{"a", "b"}, WithOneReturn())
	assert.ErrorIs(t, err, errs.ErrNoTTY)
	_, err = Dropdown(ctx, "Pick", []string{}, WithOneReturn())
	assert.ErrorIs(t, err, errs.ErrNoItems)
	_, err = Dropdown(ctx, "Pick", []string{"a"}, WithOneReturn(), WithTemplate("{{"))
	assert.Error(t, err)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = Dropdown(cctx, "Pick", []string{"a"}, WithOneReturn())
	assert.ErrorIs(t, err, context.Canceled)
}

func TestTypedDefaultSeedsInteractiveDropdown(t *testing.T) {
	// the typed default is the first row, and Enter confirms it
	servers := []server{{"alpha", 1}, {"beta", 2}, {"gamma", 3}}
	terst.UseKeys(t).Type(terst.KeyEnter)
	got, err := Dropdown(t.Context(), "Pick", servers,
		option.WithDefault(server{"gamma", 3}))
	assert.NoError(t, err)
	assert.Equal(t, servers[2], got)

	_, err = Dropdown(t.Context(), "Pick", servers,
		option.WithDefault(server{"nope", 3}))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
}

func TestEqualValues(t *testing.T) {
	type wrap struct{ V any }
	assert.True(t, (*new(choiceTarget)).equalValues(wrap{[]int{1}}, wrap{[]int{1}}))
	assert.True(t, !(*new(choiceTarget)).equalValues(wrap{[]int{1}}, wrap{[]int{2}}))
	assert.True(t, !(*new(choiceTarget)).equalValues(1, int64(1)))
	assert.True(t, (*new(choiceTarget)).equalValues(nil, nil))
	assert.True(t, !(*new(choiceTarget)).equalValues(nil, 0))
	_ = fmt.Sprint()
}

// scriptedTerminal is a terminal of a fixed width and ten rows that a themed
// prompt acquires. Each key is one read; without keys, reads block until the
// test ends. restored reports a released terminal.
type scriptedTerminal struct {
	in       io.Reader
	out      *bytes.Buffer
	restored *bool
	width    int
}

// scripted installs th for the test and scripts a terminal for one prompt.
func scripted(t *testing.T, th theme.Theme, width int, keys ...string) *scriptedTerminal {
	t.Helper()
	terst.UseTheme(t, th)
	s := &scriptedTerminal{out: &bytes.Buffer{}, restored: new(bool), width: width}
	if len(keys) == 0 {
		s.in = terst.NewKeys(t)
		return s
	}
	s.in = terst.NewKeys(t, keys...)
	return s
}

// makeTermIO exposes scripted terminal dimensions and records restoration.
func (s *scriptedTerminal) makeTermIO(in io.Reader, out io.Writer) (*term.IO, error) {
	restore := func() error { *s.restored = true; return nil }
	return &term.IO{In: in, Out: out, Width: s.width, Height: 10, Restore: restore}, nil
}

func TestDropdownConfirmClearsFrameOnce(t *testing.T) {
	terst.UseASCII(t)
	keys, out := terst.UseFrames(t)
	keys.Send(terst.KeyEnter)
	_, err := Dropdown(t.Context(), "Fruit", fruits, option.OpT(func(d *dropdown) error {
		d.makeTermIO = terst.FixedTermIO(40, 10)
		return nil
	}))
	assert.NoError(t, err)
	out.AwaitOutput("+ Fruit · apple")
	assert.Equal(t, ""+
		"\r? Fruit\n\r| > apple\n\r|   banana\n\r|   cherry\n\r| ^v move · type filter\n\r"+
		"\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"+
		"+ Fruit · apple\n", out.String())
}

func TestNewDropdownUsesDefaultIO(t *testing.T) {
	in := bytes.NewBufferString("in")
	out := &bytes.Buffer{}
	terst.UseIO(t, in, out)

	d := newDropdown(t.Context())
	assert.Equal(t, in, d.in)
	assert.Equal(t, out, d.out)
}
