// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/internal/trie"
	"github.com/nfx/go-tui/theme"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func waitForDropdownOutput(t *testing.T, out <-chan string, want string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-out:
			if got == want {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for dropdown output %q", want)
		}
	}
}

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

type blockingByteReader struct {
	ch      chan []byte
	waiters atomic.Int32
}

func newBlockingByteReader() *blockingByteReader {
	return &blockingByteReader{
		ch: make(chan []byte),
	}
}

func (r *blockingByteReader) Read(p []byte) (int, error) {
	r.waiters.Add(1)
	chunk, ok := <-r.ch
	r.waiters.Add(-1)
	if !ok {
		return 0, io.EOF
	}
	if len(chunk) == 0 || len(p) == 0 {
		return 0, nil
	}
	n := copy(p, chunk)
	return n, nil
}

func (r *blockingByteReader) SendByte(b byte) {
	r.SendBytes([]byte{b})
}

func (r *blockingByteReader) SendBytes(bs []byte) {
	cp := make([]byte, len(bs))
	copy(cp, bs)
	r.ch <- cp
}

func (r *blockingByteReader) Waiters() int {
	return int(r.waiters.Load())
}

func (r *blockingByteReader) Close() {
	close(r.ch)
}

// testIOforDropdown provides channel-driven prompt I/O at a fixed terminal size.
func testIOforDropdown(t *testing.T, width, height int, o ...option.Opt) (*term.ChanIO, option.Opt) {
	t.Helper()
	terst.UseTheme(t, asciiTheme(false))
	ctx, cancel := context.WithCancel(t.Context())
	cio := &term.ChanIO{
		Ctx: ctx,
		In:  make(chan string),
		Out: make(chan string),
	}
	t.Cleanup(func() {
		cancel()
		close(cio.In)
		close(cio.Out)
	})
	return cio, WithOptions(append(option.Opts{
		WithInput(cio),
		WithOutput(cio),
		option.OpT(func(d *dropdown) error {
			d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     out,
					Width:   width,
					Height:  height,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	}, o...,
	)...)
}

func confirmForTest(t *testing.T) (in, out chan string, result chan bool) {
	t.Helper()
	cio, opts := testIOforDropdown(t, 80, 120)
	result = make(chan bool)
	go func() {
		defer close(result)
		result <- Confirm(cio.Ctx, "Are you sure?", opts)
	}()
	return cio.In, cio.Out, result
}

func TestSimpleCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t, "\r? Are you sure?\n\r| > Yes\n\r|   No\n\r| ^v move · type filter\n\r", <-out)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "+ Are you sure? · Yes\n", waitForDropdownOutput(t, out, "+ Are you sure? · Yes\n"))
	assert.Equal(t, true, <-res)
}

func TestDenyCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t, "\r? Are you sure?\n\r| > Yes\n\r|   No\n\r| ^v move · type filter\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Are you sure?\n\r|   Yes\n\r| > No\n\r| ^v move · type filter\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "+ Are you sure? · No\n", waitForDropdownOutput(t, out, "+ Are you sure? · No\n"))
	assert.Equal(t, false, <-res)
}

func TestDownAndUpCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t, "\r? Are you sure?\n\r| > Yes\n\r|   No\n\r| ^v move · type filter\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	<-out                // frame render
	in <- "\x1b\x5b\x41" // up
	<-out                // frame render
	in <- "\x0d"         // enter
	<-out                // clear
	assert.Equal(t, "+ Are you sure? · Yes\n", waitForDropdownOutput(t, out, "+ Are you sure? · Yes\n"))
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
func overflowForTest(t *testing.T) (in, out chan string, result chan picked) {
	t.Helper()
	// room for the label, two items and the count, with the row below the
	// frame free; the key hint shows once there is no count
	cio, opts := testIOforDropdown(t, 30, 5)
	result = make(chan picked, 1)
	go func() {
		v, err := Dropdown(cio.Ctx, "Pick letter", []string{
			"A", "B", "C", "D", "E",
		}, opts)
		result <- picked{v, err}
	}()
	return cio.In, cio.Out, result
}

func TestMoreItems(t *testing.T) {
	in, out, res := overflowForTest(t)
	assert.Equal(t, "\r? Pick letter\n\r| > A\n\r|   B\n\r| 3 more · ^v move · type fi…\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > B\n\r|   C\n\r| 2 more · ^v move · type fi…\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > C\n\r|   D\n\r| 1 more · ^v move · type fi…\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	// the key hint takes the row of the count
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > D\n\r|   E\n\r| ^v move · type filter\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r|   D\n\r| > E\n\r| ^v move · type filter\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down, no more items, might bell
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r|   D\n\r| > E\n\r| ^v move · type filter\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x41" // up
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > D\n\r|   E\n\r| ^v move · type filter\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "+ Pick letter · D\n", waitForDropdownOutput(t, out, "+ Pick letter · D\n"))
	assertPicked(t, "D", res)
}

func TestMoreItemsUp(t *testing.T) {
	in, out, res := overflowForTest(t)
	assert.Equal(t, "\r? Pick letter\n\r| > A\n\r|   B\n\r| 3 more · ^v move · type fi…\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > B\n\r|   C\n\r| 2 more · ^v move · type fi…\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x41" // up
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Pick letter\n\r| > A\n\r|   B\n\r| 3 more · ^v move · type fi…\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "+ Pick letter · A\n", waitForDropdownOutput(t, out, "+ Pick letter · A\n"))
	assertPicked(t, "A", res)
}

// otherDropdownForTest starts a dropdown for filtering and clipping checks.
func otherDropdownForTest(t *testing.T) (in, out chan string, result chan picked) {
	t.Helper()
	// room for the label, two items and the count, as in overflowForTest
	cio, opts := testIOforDropdown(t, 30, 5)
	result = make(chan picked, 1)
	go func() {
		v, err := Dropdown(cio.Ctx, "Neque porro", []string{
			"Lorem ipsum",
			"dolor sit amet",
			"adipiscing elit",
			"Quisque porttitor",
			"condimentum libero",
		}, opts)
		result <- picked{v, err}
	}()
	return cio.In, cio.Out, result
}

func TestDropdownFiltering(t *testing.T) {
	in, out, res := otherDropdownForTest(t)
	assert.Equal(
		t,
		"\r? Neque porro\n\r| > Lorem ipsum\n\r|   dolor sit amet\n\r| 3 more · ^v move · type fi…\n\r",
		<-out,
	)
	in <- "c"
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Neque porro\n\r| > condimentum libero\n\r| ^v move · type filter\n\r",
		<-out,
	)
	in <- "\x7f" // backspace
	assert.Equal(
		t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r"+"\r? Neque porro\n\r| > Lorem ipsum\n\r|   dolor sit amet\n\r| 3 more · ^v move · type fi…\n\r",
		<-out,
	)
	in <- "l"
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Neque porro\n\r| > Lorem ipsum\n\r|   condimentum libero\n\r| ^v move · type filter\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\r? Neque porro\n\r|   Lorem ipsum\n\r| > condimentum libero\n\r| ^v move · type filter\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(
		t,
		"+ Neque porro · condimentum libero\n",
		waitForDropdownOutput(t, out, "+ Neque porro · condimentum libero\n"),
	)
	assertPicked(t, "condimentum libero", res)
}

func dropdownKVForTest(t *testing.T, items map[string]int) (in, out chan string, result chan struct {
	key   string
	value int
	err   error
}) {
	t.Helper()
	cio, opts := testIOforDropdown(t, 80, 120)
	result = make(chan struct {
		key   string
		value int
		err   error
	})
	go func() {
		defer close(result)
		key, value, err := DropdownKV(cio.Ctx, "Select item", items, opts)
		result <- struct {
			key   string
			value int
			err   error
		}{key, value, err}
	}()
	return cio.In, cio.Out, result
}

func TestDropdownKVEmpty(t *testing.T) {
	_, _, res := dropdownKVForTest(t, map[string]int{})
	result := <-res
	assert.Equal(t, "", result.key)
	assert.Equal(t, 0, result.value)
	assert.Equal(t, ErrNoItems, result.err)
}

func TestDropdownKVSingleItem(t *testing.T) {
	in, out, res := dropdownKVForTest(t, map[string]int{"apple": 42})
	assert.Equal(t, "\r? Select item\n\r| > apple\n\r| ^v move · type filter\n\r", <-out)
	in <- "\x0d" // enter
	<-out
	assert.Equal(t, "+ Select item · apple\n", waitForDropdownOutput(t, out, "+ Select item · apple\n"))
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
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	clear5 := "\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"
	assert.Equal(t,
		clear5+"\r? Select item\n\r|   apple\n\r| > banana\n\r|   zebra\n\r| ^v move · type filter\n\r",
		<-out)
	in <- "\x0d" // enter
	assert.Equal(t, clear5, <-out)
	assert.Equal(t, "+ Select item · banana\n", waitForDropdownOutput(t, out, "+ Select item · banana\n"))
	result := <-res
	assert.Equal(t, "banana", result.key)
	assert.Equal(t, 3, result.value)
	assert.NoError(t, result.err)
}

func TestDropdownRunSelectsWithArrow(t *testing.T) {
	reader := &terst.ChunkReader{Chunks: [][]byte{
		{0x1b, 0x5b, 0x42},
		{byte(term.KeyEnter)},
	}}
	d := newDropdown(t.Context())
	d.Items = []any{"one", "two"}
	d.in = reader
	d.out = &bytes.Buffer{}
	d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
		return &term.IO{
			In:      in,
			Out:     out,
			Width:   20,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	idx, err := d.dropdownIndex(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownRunIgnoresSpecialKeys(t *testing.T) {
	reader := &terst.ChunkReader{Chunks: [][]byte{
		[]byte("\x1b[3~"),
		[]byte("\x1bx"),
		{0x1b, 0x1b, 0x5b, 0x42}, // Esc, then down
		{byte(term.KeyEnter)},
	}}
	d := newDropdown(t.Context())
	d.Items = []any{"one", "two"}
	d.in = reader
	d.out = &bytes.Buffer{}
	d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
		return &term.IO{
			In:      in,
			Out:     out,
			Width:   20,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
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
	d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
		return &term.IO{
			In:      in,
			Out:     out,
			Width:   20,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	idx, err := d.dropdownIndex(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownRunOneReturn(t *testing.T) {
	reader := &terst.ChunkReader{Chunks: [][]byte{
		{'b'},
	}}
	d := newDropdown(t.Context())
	d.Items = []any{"alpha", "beta"}
	d.OneReturn = true
	d.in = reader
	d.out = &bytes.Buffer{}
	d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
		return &term.IO{
			In:      in,
			Out:     out,
			Width:   20,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	idx, err := d.dropdownIndex(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownOptionHelpers(t *testing.T) {
	d := newDropdown(t.Context())

	for _, opt := range []option.Opt{
		WithOneReturn(),
		WithHide(),
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

func TestDropdownContextAndIOSetters(t *testing.T) {
	d := newDropdown(t.Context())
	in := bytes.NewBufferString("input")
	d.SetReader(in)
	assert.Equal(t, in, d.in)
	out := &bytes.Buffer{}
	d.SetWriter(out)
	assert.Equal(t, out, d.out)
}

func TestDropdownSetWriterUsesTuiViewport(t *testing.T) {
	ctx := t.Context()
	cio := term.NewUnstartedIO(ctx, 10, 2, 0)
	tui := &term.Tui{Ctx: ctx, IO: &term.IO{Out: cio}}
	d := newDropdown(t.Context())
	d.SetWriter(tui)
	_, ok := d.out.(*term.Viewport)
	assert.True(t, ok)
}

type errWriterDropdown struct{}

func (errWriterDropdown) Write(p []byte) (int, error) {
	return 0, io.EOF
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
	d.out = errWriterDropdown{}
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
		Out:     errWriterDropdown{},
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
		Out:     errWriterDropdown{},
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

func captureOutput(cio *term.ChanIO) <-chan string {
	out := make(chan string, 32)
	go func() {
		for line := range cio.Out {
			out <- line
		}
		close(out)
	}()
	return out
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

// notifyItemsLoaded must be applied after WithOutput.
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
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for dropdown items")
	}
}

func waitForOutputContaining(t *testing.T, out <-chan string, substr string, timeout time.Duration) string {
	t.Helper()
	var buf strings.Builder
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case chunk, ok := <-out:
			if !ok {
				return buf.String()
			}
			buf.WriteString(chunk)
			if strings.Contains(buf.String(), substr) {
				return buf.String()
			}
		case <-timer.C:
			t.Fatalf("timeout waiting for %q, collected %q", substr, buf.String())
		}
	}
}

func assertNoWaitersFor(t *testing.T, r *blockingByteReader, d time.Duration) {
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
		d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			return &term.IO{In: in, Out: out, Width: 20, Height: 6, Restore: func() error { return nil }}, nil
		}
		return nil
	})
}

func TestDropdownIndexUniqueDefaultReturnsOriginalIndex(t *testing.T) {
	i, err := DropdownIndex(t.Context(), "pick", []any{"xxx", "b", "a"},
		WithInput(bytes.NewBuffer(nil)), WithOutput(io.Discard),
		fixedTermOpt(), WithDefault("a"))
	assert.NoError(t, err)
	assert.Equal(t, 2, i)

	v, err := Dropdown(t.Context(), "pick", []string{"xxx", "b", "a"},
		WithInput(bytes.NewBuffer(nil)), WithOutput(io.Discard),
		fixedTermOpt(), WithDefault("a"))
	assert.NoError(t, err)
	assert.Equal(t, "a", v)
}

func TestDropdownFilterEventsIgnoreRankingOrder(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithDefault("ap")(d))
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

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Len()
}

func TestDropdownEagerRedrawsOnResizeWhileIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		cancel()
		_ = reader.Close() //nolint:errcheck // test cleanup
		_ = writer.Close() //nolint:errcheck // test cleanup
	})
	out := &lockedBuffer{}
	resize := make(chan struct{})
	resCh := make(chan string, 1)
	go func() {
		value, _ := Dropdown(ctx, "Pick", []string{"a", "b"}, WithOptions( //nolint:errcheck // test cleanup
			WithInput(reader),
			WithOutput(out),
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
	waitUntil(t, func() bool { return out.Len() > 0 })
	before := out.Len()
	select {
	case resize <- struct{}{}:
	case <-time.After(time.Second):
		t.Fatal("eager dropdown did not receive resize while idle")
	}
	waitUntil(t, func() bool { return out.Len() > before })
	// the retained reader still delivers the next key
	_, err := writer.Write([]byte{term.KeyEnter})
	assert.NoError(t, err)
	select {
	case v := <-resCh:
		assert.Equal(t, "a", v)
	case <-time.After(2 * time.Second):
		t.Fatal("dropdown did not accept key after resize")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDropdownTypedDefaults(t *testing.T) {
	io := terst.Quiet(t)
	ctx := t.Context()

	servers := []server{{"alpha", 1}, {"beta", 2}}
	got, err := Dropdown(ctx, "Pick", servers, io, WithDefault(server{"beta", 2}))
	assert.NoError(t, err)
	assert.Equal(t, servers[1], got)

	// identical labels, distinct values
	same := []tagged{{"x", 1}, {"x", 2}}
	gotTagged, err := Dropdown(ctx, "Pick", same, io, WithDefault(tagged{"x", 2}))
	assert.NoError(t, err)
	assert.Equal(t, 2, gotTagged.ID)

	// pointer identity
	a, b := &server{"same", 1}, &server{"same", 1}
	ptr, err := Dropdown(ctx, "Pick", []*server{a, b}, io, WithDefault(b))
	assert.NoError(t, err)
	assert.True(t, ptr == b)

	// non-comparable items
	slices, err := Dropdown(ctx, "Pick", [][]int{{1}, {2, 3}}, io, WithDefault([]int{2, 3}))
	assert.NoError(t, err)
	assert.Equal(t, []int{2, 3}, slices)
	maps, err := Dropdown(ctx, "Pick", []map[string]int{{"a": 1}, {"b": 2}}, io, WithDefault(map[string]int{"b": 2}))
	assert.NoError(t, err)
	assert.Equal(t, map[string]int{"b": 2}, maps)

	idx, err := DropdownIndex(ctx, "Pick", []any{1, "two", 3.5}, io, WithDefault(3.5))
	assert.NoError(t, err)
	assert.Equal(t, 2, idx)

	// wrong type, missing value, duplicates
	_, err = Dropdown(ctx, "Pick", []int64{1, 2}, io, WithDefault(2))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", servers, io, WithDefault(server{"gamma", 3}))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []int{1, 2, 1}, io, WithDefault(1))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", servers, io, WithDefault(true))
	assert.ErrorIs(t, err, ErrInvalidOption)

	// nil
	var none *server
	nilPtr, err := Dropdown(ctx, "Pick", []*server{a, none}, io, WithDefault(nil))
	assert.NoError(t, err)
	assert.True(t, nilPtr == nil)
	_, err = Dropdown(ctx, "Pick", []*server{a}, io, WithDefault(nil))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []int{1}, io, WithDefault(nil))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []*server{none, none}, io, WithDefault(nil))
	assert.ErrorIs(t, err, ErrInvalidOption)

	// functions cannot be identified, NaN does not equal itself
	fn := func() {}
	_, err = Dropdown(ctx, "Pick", []func(){fn}, io, WithDefault(fn))
	assert.ErrorIs(t, err, ErrInvalidOption)
	nan := 0.0
	nan /= nan
	_, err = Dropdown(ctx, "Pick", []float64{nan}, io, WithDefault(nan))
	assert.ErrorIs(t, err, ErrInvalidOption)

	_, err = Dropdown(ctx, "Pick", servers, io)
	assert.ErrorIs(t, err, ErrNoTTY)
}

func TestDropdownStringDefaults(t *testing.T) {
	io := terst.Quiet(t)
	ctx := t.Context()

	got, err := Dropdown(ctx, "Pick", []string{"ab", "a"}, io, WithDefault("a"))
	assert.NoError(t, err)
	assert.Equal(t, "a", got)

	got, err = Dropdown(ctx, "Pick", []string{"alpha", "beta"}, io, WithDefault("be"))
	assert.NoError(t, err)
	assert.Equal(t, "beta", got)

	_, err = Dropdown(ctx, "Pick", []string{"alpha", "apple"}, io, WithDefault("a"))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []string{"alpha", "apple"}, io, WithDefault("zz"))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = Dropdown(ctx, "Pick", []string{"alpha", "apple"}, io, WithDefault(""))
	assert.ErrorIs(t, err, ErrInvalidOption)
	// an exact duplicate is ambiguous, whatever a hint could do
	_, err = Dropdown(ctx, "Pick", []string{"a", "a", "ab"}, io, WithDefault("a"))
	assert.ErrorIs(t, err, ErrInvalidOption)
	got, err = Dropdown(ctx, "Pick", []string{"", "x"}, io, WithDefault(""))
	assert.NoError(t, err)
	assert.Equal(t, "", got)

	// a hint finds a struct by its label
	servers := []server{{"alpha", 1}, {"beta", 2}}
	s, err := Dropdown(ctx, "Pick", servers, io, WithDefault("bet"))
	assert.NoError(t, err)
	assert.Equal(t, servers[1], s)
}

func TestDropdownKVDefault(t *testing.T) {
	io := terst.Quiet(t)
	items := map[string]int{"one": 1, "two": 2}
	k, v, err := DropdownKV(t.Context(), "Pick", items, io, WithDefault("two"))
	assert.NoError(t, err)
	assert.Equal(t, "two", k)
	assert.Equal(t, 2, v)

	// the value is not a key lookup
	_, _, err = DropdownKV(t.Context(), "Pick", map[int]int{1: 2, 2: 1}, io, WithDefault("x"))
	assert.ErrorIs(t, err, ErrInvalidOption)
	k2, v2, err := DropdownKV(t.Context(), "Pick", map[int]int{1: 2, 2: 1}, io, WithDefault(2))
	assert.NoError(t, err)
	assert.Equal(t, 2, k2)
	assert.Equal(t, 1, v2)
}

func TestDropdownOneReturn(t *testing.T) {
	io := terst.Quiet(t)
	ctx := t.Context()
	got, err := Dropdown(ctx, "Pick", []string{"only"}, io, WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, "only", got)
	// the shortcut wins over a hint
	got, err = Dropdown(ctx, "Pick", []string{"only"}, io, WithOneReturn(), WithDefault("zzz"))
	assert.NoError(t, err)
	assert.Equal(t, "only", got)
	_, err = Dropdown(ctx, "Pick", []string{"a", "b"}, io, WithOneReturn())
	assert.ErrorIs(t, err, ErrNoTTY)
	_, err = Dropdown(ctx, "Pick", []string{}, io, WithOneReturn())
	assert.ErrorIs(t, err, ErrNoItems)
	_, err = Dropdown(ctx, "Pick", []string{"a"}, io, WithOneReturn(), WithTemplate("{{"))
	assert.Error(t, err)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = Dropdown(cctx, "Pick", []string{"a"}, io, WithOneReturn())
	assert.ErrorIs(t, err, context.Canceled)
}

func TestTypedDefaultSeedsInteractiveDropdown(t *testing.T) {
	// the typed default is the first row, and Enter confirms it
	servers := []server{{"alpha", 1}, {"beta", 2}, {"gamma", 3}}
	var out bytes.Buffer
	got, err := Dropdown(t.Context(), "Pick", servers,
		WithInput(strings.NewReader("\r")), WithOutput(&out), WithDefault(server{"gamma", 3}))
	assert.NoError(t, err)
	assert.Equal(t, servers[2], got)

	_, err = Dropdown(t.Context(), "Pick", servers,
		WithInput(strings.NewReader("\r")), WithOutput(&out), WithDefault(server{"nope", 3}))
	assert.ErrorIs(t, err, ErrInvalidOption)
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
		blocking := newBlockingByteReader()
		t.Cleanup(blocking.Close)
		s.in = blocking
		return s
	}
	var chunks [][]byte
	for _, k := range keys {
		chunks = append(chunks, []byte(k))
	}
	s.in = &terst.ChunkReader{Chunks: chunks}
	return s
}

// makeTermIO exposes scripted terminal dimensions and records restoration.
func (s *scriptedTerminal) makeTermIO(in io.Reader, out io.Writer) (*term.IO, error) {
	restore := func() error { *s.restored = true; return nil }
	return &term.IO{In: in, Out: out, Width: s.width, Height: 10, Restore: restore}, nil
}

func TestDropdownConfirmClearsFrameOnce(t *testing.T) {
	terst.UseTheme(t, asciiTheme(false))
	var out bytes.Buffer
	_, err := Dropdown(t.Context(), "Fruit", fruits, WithOutput(&out),
		WithInput(&terst.ChunkReader{Chunks: [][]byte{[]byte("\r")}}),
		option.OpT(func(d *dropdown) error {
			d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{In: in, Out: out, Width: 40, Height: 10, Restore: func() error { return nil }}, nil
			}
			return nil
		}))
	assert.NoError(t, err)
	assert.Equal(t, ""+
		"\r? Fruit\n\r| > apple\n\r|   banana\n\r|   cherry\n\r| ^v move · type filter\n\r"+
		"\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"+
		"+ Fruit · apple\n", out.String())
}
