// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
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

func testIOforDropdown(t *testing.T, width, height int, o ...opt) (*chanIO, opt) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cio := &chanIO{
		ctx: ctx,
		In:  make(chan string),
		Out: make(chan string),
	}
	t.Cleanup(func() {
		cancel()
		close(cio.In)
		close(cio.Out)
	})
	return cio, WithOptions(append(opts{
		WithInput(cio),
		WithOutput(cio),
		WithContext(ctx),
		opT(func(d *dropdown) error {
			d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   width,
					Height:  height,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
		WithLabelTemplate("{{ . }}"),
		WithActiveItemTemplate("+ {{ . }}"),
		WithInactiveItemTemplate("- {{ . }}"),
		WithMoreItemsTemplate("~ {{ .More }} of {{ .Total }} more"),
		WithAnswerTemplate("{{ .Label }}: {{ .Answer }}"),
	}, o...,
	)...)
}

func confirmForTest(t *testing.T) (in, out chan string, result chan bool) {
	t.Helper()
	cio, opts := testIOforDropdown(t, 80, 120)
	result = make(chan bool)
	go func() {
		defer close(result)
		result <- Confirm("Are you sure?", opts)
	}()
	return cio.In, cio.Out, result
}

func TestSimpleCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t,
		"\rAre you sure? + Yes\n\r              - No\n\r",
		<-out)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r", <-out)
	assert.Equal(t, "Are you sure?: Yes\n", waitForDropdownOutput(t, out, "Are you sure?: Yes\n"))
	assert.Equal(t, true, <-res)
}

func TestDenyCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t,
		"\rAre you sure? + Yes\n\r              - No\n\r",
		<-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(t,
		"\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r\rAre you sure? - Yes\n\r              + No\n\r",
		<-out)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r", <-out)
	assert.Equal(t, "Are you sure?: No\n", waitForDropdownOutput(t, out, "Are you sure?: No\n"))
	assert.Equal(t, false, <-res)
}

func TestDownAndUpCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t,
		"\rAre you sure? + Yes\n\r              - No\n\r",
		<-out)
	in <- "\x1b\x5b\x42" // down
	<-out                // frame render
	in <- "\x1b\x5b\x41" // up
	<-out                // frame render
	in <- "\x0d"         // enter
	<-out                // clear
	assert.Equal(t, "Are you sure?: Yes\n", waitForDropdownOutput(t, out, "Are you sure?: Yes\n"))
	assert.Equal(t, true, <-res)
}

func overflowForTest(t *testing.T) (in, out chan string, result chan string) {
	t.Helper()
	cio, opts := testIOforDropdown(t, 12, 4)
	result = make(chan string)
	go func() {
		defer close(result)
		v, err := Dropdown("Pick letter", []string{
			"A", "B", "C", "D", "E",
		}, opts)
		assert.NoError(t, err)
		result <- v
	}()
	return cio.In, cio.Out, result
}

func TestMoreItems(t *testing.T) {
	in, out, res := overflowForTest(t)
	assert.Equal(t, "\rPick letter \n\r+ A\n\r- B\n\r~ 3 of 5 more\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rPick letter \n\r+ B\n\r- C\n\r~ 2 of 5 more\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rPick letter \n\r+ C\n\r- D\n\r~ 1 of 5 more\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rPick letter \n\r+ D\n\r- E\n\r\n\r",
		<-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rPick letter \n\r- D\n\r+ E\n\r\n\r",
		<-out)
	in <- "\x1b\x5b\x42" // down, no more items, might bell
	assert.Equal(t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rPick letter \n\r- D\n\r+ E\n\r\n\r",
		<-out)
	in <- "\x1b\x5b\x41" // up
	assert.Equal(t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rPick letter \n\r+ D\n\r- E\n\r\n\r",
		<-out)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "Pick letter: D\n", waitForDropdownOutput(t, out, "Pick letter: D\n"))
	assert.Equal(t, "D", <-res)
}

func TestMoreItemsUp(t *testing.T) {
	in, out, res := overflowForTest(t)
	assert.Equal(t, "\rPick letter \n\r+ A\n\r- B\n\r~ 3 of 5 more\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rPick letter \n\r+ B\n\r- C\n\r~ 2 of 5 more\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x41" // up
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rPick letter \n\r+ A\n\r- B\n\r~ 3 of 5 more\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "Pick letter: A\n", waitForDropdownOutput(t, out, "Pick letter: A\n"))
	assert.Equal(t, "A", <-res)
}

func otherDropdownForTest(t *testing.T) (in, out chan string, result chan string) {
	t.Helper()
	cio, opts := testIOforDropdown(t, 12, 4)
	result = make(chan string)
	go func() {
		defer close(result)
		v, err := Dropdown("Neque porro", []string{
			"Lorem ipsum",
			"dolor sit amet",
			"adipiscing elit",
			"Quisque porttitor",
			"condimentum libero",
		}, opts)
		assert.NoError(t, err)
		result <- v
	}()
	return cio.In, cio.Out, result
}

func TestDropdownFiltering(t *testing.T) {
	in, out, res := otherDropdownForTest(t)
	assert.Equal(t, "\rNeque porro \n\r+ Lorem i…\n\r- dolor s…\n\r~ 3 of 5 more\n\r", <-out)
	in <- "c"
	assert.Equal(t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rNeque porro \n\r+ condime…\n\r",
		<-out)
	in <- "\x7f" // backspace
	assert.Equal(t,
		"\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r\rNeque porro \n\r+ Lorem i…\n\r- dolor s…\n\r~ 3 of 5 more\n\r",
		<-out)
	in <- "l"
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rNeque porro \n\r+ Lorem i…\n\r- condime…\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r\rNeque porro \n\r- Lorem i…\n\r+ condime…\n\r",
		<-out)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r", <-out)
	assert.Equal(
		t,
		"Neque porro: condimentum libero\n",
		waitForDropdownOutput(t, out, "Neque porro: condimentum libero\n"),
	)
	assert.Equal(t, "condimentum libero", <-res)
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
		key, value, err := DropdownKV("Select item", items, opts)
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
	assert.Equal(t, "\rSelect item + {apple 42}\n\r", <-out)
	in <- "\x0d" // enter
	<-out
	assert.Equal(t, "Select item: {apple 42}\n", waitForDropdownOutput(t, out, "Select item: {apple 42}\n"))
	result := <-res
	assert.Equal(t, "apple", result.key)
	assert.Equal(t, 42, result.value)
	assert.NoError(t, result.err)
}

func TestDropdownKVMultipleItems(t *testing.T) {
	in, out, res := dropdownKVForTest(t, map[string]int{"zebra": 1, "apple": 2, "banana": 3})
	assert.Equal(t, "\rSelect item + {apple 2}\n\r            - {banana 3}\n\r            - {zebra 1}\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r\rSelect item - {apple 2}\n\r            + {banana 3}\n\r            - {zebra 1}\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r", <-out)
	assert.Equal(t, "Select item: {banana 3}\n", waitForDropdownOutput(t, out, "Select item: {banana 3}\n"))
	result := <-res
	assert.Equal(t, "banana", result.key)
	assert.Equal(t, 3, result.value)
	assert.NoError(t, result.err)
}

func TestDropdownLazyClearsOnlyRenderedArea(t *testing.T) {
	d := newDropdown()
	assert.NoError(t, d.parseTemplates())
	d.trie = newTrie()

	var (
		frame  bytes.Buffer
		output bytes.Buffer
	)
	tio := &termIO{
		in:      bytes.NewBuffer(nil),
		out:     &output,
		Width:   20,
		Height:  5,
		Restore: func() error { return nil },
	}
	space := 2
	err := d.loadItem(tio, &frame, itPair{item: "one"}, true, space)
	assert.NoError(t, err)

	var expected bytes.Buffer
	assert.NoError(t, tio.clear(space, &expected))
	assert.Equal(t, expected.String(), output.String())
}

func TestDropdownRunSelectsWithArrow(t *testing.T) {
	reader := &chunkReader{chunks: [][]byte{
		{0x1b, 0x5b, 0x42},
		{byte(keyEnter)},
	}}
	d := newDropdown()
	d.Items = []any{"one", "two"}
	d.in = reader
	d.out = &bytes.Buffer{}
	d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   20,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	idx, err := d.dropdownIndex()
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownRunIgnoresSpecialKeys(t *testing.T) {
	reader := &chunkReader{chunks: [][]byte{
		[]byte("\x1b[3~"),
		[]byte("\x1bx"),
		{0x1b, 0x1b, 0x5b, 0x42}, // Esc, then down
		{byte(keyEnter)},
	}}
	d := newDropdown()
	d.Items = []any{"one", "two"}
	d.in = reader
	d.out = &bytes.Buffer{}
	d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   20,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	idx, err := d.dropdownIndex()
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
	assert.Equal(t, "", string(d.typed))
}

func TestDropdownRunConsumesInputEvents(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"one", "two"}
	d.in = bytes.NewBuffer(nil)
	d.out = &bytes.Buffer{}
	input := make(chan dropdownInputEvent, 1)
	input <- dropdownInputConfirmed{Index: 1}
	close(input)
	d.input = input
	d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   20,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	idx, err := d.dropdownIndex()
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownRunOneReturn(t *testing.T) {
	reader := &chunkReader{chunks: [][]byte{
		{'b'},
	}}
	d := newDropdown()
	d.Items = []any{"alpha", "beta"}
	d.OneReturn = true
	d.in = reader
	d.out = &bytes.Buffer{}
	d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   20,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	idx, err := d.dropdownIndex()
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownPressKeyRuneAndRenderMore(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"alpha", "beta"}
	d.relevant = []int{0, 1}
	d.displayed = d.relevant
	d.trie = newTrie()
	d.trie.Add("alpha", 0)
	d.trie.Add("beta", 1)

	tio := newTestTermIO(20, 6)

	assert.Equal(t, 0, d.pressKeyRune(tio, keyEnter, len(d.displayed), 4))

	d.selected = 1
	d.pressKeyRune(tio, '↑', len(d.displayed), 4)
	d.pressKeyRune(tio, '↓', len(d.displayed), 4)

	d.typed = []rune("ab")
	d.pressKeyRune(tio, 0x7f, len(d.displayed), 4)

	d.OneReturn = true
	d.typed = nil
	d.trie = newTrie()
	d.trie.Add("abc", 0)
	d.Items = []any{"abc"}
	d.relevant = []int{0}
	d.displayed = d.relevant
	assert.True(t, d.pressAny('a', len(d.displayed), 4))

	var frame bytes.Buffer
	assert.NoError(t, d.clearFrame(tio, &frame, 0))

	d.relevant = []int{0, 1, 2, 3}
	d.displayed = d.relevant[:2]
	d.Items = append(d.Items, 3, 4)
	assert.NoError(t, d.parseTemplates())
	buf, longest, err := d.renderMore(4, 2, 0)
	assert.NoError(t, err)
	assert.True(t, longest > 0)
	assert.True(t, len(buf) > 0)
}

func TestDropdownRenderInitAndItems(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"alpha", "beta", "gamma"}
	d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return newTestTermIO(20, 6), nil
	}
	io := newTestTermIO(20, 6)

	assert.NoError(t, d.parseTemplates())
	longest, err := d.renderInit(io)
	assert.NoError(t, err)
	assert.True(t, longest > 0)
	assert.True(t, len(d.displayed) > 0)

	item := d.renderItem(io, 0, d.displayed[0])
	assert.True(t, len(item) > 0)
}

func TestDropdownRenderInit_activeItemOverflowTriggersLabelNewLine(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"ab", "cd"}
	// active template is much wider than inactive
	d.ActiveItemTemplate = `{{ cyan "→ selected: " (label .) " (details)" }}`
	d.InactiveItemTemplate = `{{ dim (label .) }}`
	// terminal is wide enough for label + inactive, but not for label + active
	tio := newTestTermIO(20, 6)
	assert.NoError(t, d.parseTemplates())
	longest, err := d.renderInit(tio)
	assert.NoError(t, err)
	// active rendering of "→ selected: ab (details)" is ~25 chars, wider than inactive "ab" (~2 chars)
	assert.True(t, longest > 10)
	var buf bytes.Buffer
	prefix := d.renderLabel(&buf, tio, longest)
	assert.Equal(t, 0, prefix)
	assert.True(t, d.LabelNewLine)
}

func TestDropdownRenderInitEmitsEvents(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"alpha", "beta"}
	assert.NoError(t, d.parseTemplates())
	io := newTestTermIO(20, 6)
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	_, err := d.renderInit(io)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(events))
	init, ok := events[0].(dropdownInit)
	assert.True(t, ok)
	assert.Equal(t, "Select from list", init.Label)
	_, ok = events[1].(dropdownAppendItem)
	assert.True(t, ok)
	_, ok = events[2].(dropdownAppendItem)
	assert.True(t, ok)
}

func TestDropdownRenderMoreAndHeight(t *testing.T) {
	d := newDropdown()
	d.Items = make([]any, 10)
	for i := range d.Items {
		d.Items[i] = i
	}
	d.displayed = []int{0, 1, 2}
	d.relevant = []int{0, 1, 2}

	assert.NoError(t, d.parseTemplates())

	buf, longest, err := d.renderMore(10, 3, 0)
	assert.NoError(t, err)
	assert.True(t, len(buf) > 0)
	assert.True(t, longest > 0)
	assert.True(t, d.height() >= 3)
}

func TestDropdownPressKeys(t *testing.T) {
	d := newDropdown()
	d.relevant = []int{0, 1, 2, 3}
	d.displayed = []int{0, 1, 2}
	d.selected = 1

	d.pressDown(len(d.displayed))
	assert.Equal(t, 1, d.offset)
	d.pressUp(len(d.displayed))
	assert.Equal(t, 0, d.selected)

	d.typed = nil
	d.trie = newTrie()
	d.relevant = []int{0}
	d.displayed = d.relevant
	d.pressBackspace(newTestTermIO(10, 4))
	assert.Equal(t, "", string(d.typed))
	assert.True(t, !d.pressAny('z', len(d.displayed), 4))
}

func TestDropdownClearFrame(t *testing.T) {
	d := newDropdown()
	io := newTestTermIO(10, 4)
	frame := &bytes.Buffer{}
	assert.NoError(t, d.clearFrame(io, frame, 2))
	buf, ok := io.out.(*bytes.Buffer)
	assert.True(t, ok)
	assert.True(t, buf.Len() > 0)
}

func TestDropdownOptionHelpers(t *testing.T) {
	d := newDropdown()

	for _, opt := range []opt{
		WithOneReturn(),
		WithHide(),
		WithFieldTemplate("Label"),
		WithTemplate(".Label"),
		WithLabelTemplate("{{.}} "),
		WithActiveItemTemplate("{{.}}>"),
		WithInactiveItemTemplate("{{.}}-"),
		WithMoreItemsTemplate("more"),
		WithAnswerTemplate("> {{.}}"),
	} {
		assert.NoError(t, opt(d))
	}
	assert.True(t, d.OneReturn)
	assert.True(t, d.Hide)
}

func TestShowAnswerWritesWhenVisible(t *testing.T) {
	d := newDropdown()
	d.Label = "Test"
	d.out = &bytes.Buffer{}
	assert.NoError(t, d.parseTemplates())
	assert.NoError(t, d.showAnswer("Test", "value"))
}

func TestDropdownPressAnyOneReturn(t *testing.T) {
	d := newDropdown()
	d.OneReturn = true
	d.trie = newTrie()
	d.trie.Add("ok", 0)

	assert.True(t, d.pressAny('o', 1, 1))
}

func TestConfirmfRunsWithFormattedAction(t *testing.T) {
	orig := confirmRunner
	defer func() { confirmRunner = orig }()
	confirmRunner = func(action string, opts ...opt) bool {
		assert.Equal(t, "Proceed with task?", action)
		return true
	}

	assert.True(t, Confirmf("Proceed with %s?", "task"))
}

func TestDefaultConfirmRunnerReturnsTrue(t *testing.T) {
	in := bytes.NewBuffer([]byte{keyEnter})
	out := &bytes.Buffer{}
	opt := opT(func(d *dropdown) error {
		d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return &termIO{
				in:      in,
				out:     out,
				Width:   20,
				Height:  6,
				Restore: func() error { return nil },
			}, nil
		}
		return nil
	})
	assert.True(t, defaultConfirmRunner("Proceed?", WithInput(in), WithOutput(out), opt))
}

func TestDefaultConfirmRunnerReturnsFalseOnError(t *testing.T) {
	opt := opT(func(d *dropdown) error {
		d.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) {
			return nil, io.EOF
		}

		return nil
	})
	assert.True(t, !defaultConfirmRunner("Proceed?", opt))
}

func TestWithTemplateSetsTemplates(t *testing.T) {
	d := newDropdown()
	assert.NoError(t, WithTemplate(".Name")(d))
	assert.Equal(t, `{{ cyan "→ " .Name }}`, d.ActiveItemTemplate)
	assert.Equal(t, `{{ dim "→ " .Name }}`, d.InactiveItemTemplate)
	assert.Equal(t, `{{ dim "✔ " .Label " …" }} {{ bold .Answer.Name }}`, d.AnswerTemplate)
}

func TestWithTemplateAddsActiveDetails(t *testing.T) {
	d := newDropdown()
	assert.NoError(t, WithTemplate(".Name", ".Type", ".Owner")(d))
	expected := `{{ cyan "→ " .Name }} {{ dim "(" (.Type) ", " (.Owner) ")" }}`
	assert.Equal(t, expected, d.ActiveItemTemplate)
}

func TestDropdownItemLabelUsesHeuristicField(t *testing.T) {
	d := newDropdown()
	item := dropdownHeuristicLabelItem{ID: 42, Name: "alpha"}
	assert.Equal(t, "alpha", d.itemLabel(item))
}

func TestDropdownItemLabelUsesDisplayNameHeuristic(t *testing.T) {
	d := newDropdown()
	item := dropdownDisplayNameLabelItem{DisplayName: "service-a"}
	assert.Equal(t, "service-a", d.itemLabel(item))
}

func TestDropdownItemLabelUsesFullNameHeuristic(t *testing.T) {
	d := newDropdown()
	item := dropdownFullNameLabelItem{FullName: "Example Service"}
	assert.Equal(t, "Example Service", d.itemLabel(item))
}

func TestDropdownItemLabelUsesSummaryHeuristic(t *testing.T) {
	d := newDropdown()
	item := dropdownSummaryLabelItem{Summary: "concise summary"}
	assert.Equal(t, "concise summary", d.itemLabel(item))
}

func TestDropdownItemLabelUsesTextHeuristic(t *testing.T) {
	d := newDropdown()
	item := dropdownTextLabelItem{Text: "plaintext value"}
	assert.Equal(t, "plaintext value", d.itemLabel(item))
}

func TestDropdownItemLabelUsesSubjectHeuristic(t *testing.T) {
	d := newDropdown()
	item := dropdownSubjectLabelItem{Subject: "subject line"}
	assert.Equal(t, "subject line", d.itemLabel(item))
}

func TestDropdownItemLabelUsesAnnotation(t *testing.T) {
	d := newDropdown()
	item := dropdownAnnotatedLabelItem{
		Name:        "fallback",
		Description: "annotated",
	}
	assert.Equal(t, "annotated", d.itemLabel(item))
}

func TestDropdownItemLabelFallsBackToStringer(t *testing.T) {
	d := newDropdown()
	item := dropdownStringerLabelItem{ID: 7}
	assert.Equal(t, "stringer-7", d.itemLabel(item))
}

func TestDropdownItemLabelFallsBackToPointerStringer(t *testing.T) {
	d := newDropdown()
	item := dropdownPointerStringerLabelItem{ID: 11}
	assert.Equal(t, "ptr-stringer-11", d.itemLabel(item))
}

func TestDropdownSetItemUsesResolvedLabelForTrie(t *testing.T) {
	d := newDropdown()
	item := dropdownHeuristicLabelItem{ID: 3, Name: "omega"}
	d.active = make([]bbuf, 1)
	d.activeWidths = make([]int, 1)
	d.inactive = make([]bbuf, 1)
	d.widths = make([]int, 1)
	d.relevant = make([]int, 1)
	d.trie = newTrie()
	assert.NoError(t, d.parseTemplates())
	assert.NoError(t, d.setItem(0, item))
	assert.Equal(t, []int{0}, d.trie.Prefix("ome"))
	assert.NotContains(t, d.inactive[0].String(), "{")
}

func TestDropdownShowAnswerUsesResolvedLabel(t *testing.T) {
	d := newDropdown()
	out := &bytes.Buffer{}
	d.out = out
	assert.NoError(t, d.parseTemplates())
	assert.NoError(t, d.showAnswer("Pick", dropdownHeuristicLabelItem{ID: 9, Name: "delta"}))
	assert.Contains(t, out.String(), "delta")
	assert.NotContains(t, out.String(), "{")
}

func TestDropdownContextAndIOSetters(t *testing.T) {
	d := newDropdown()
	ctx := t.Context()
	d.setContext(ctx)
	assert.Equal(t, ctx, d.getContext())
	in := bytes.NewBufferString("input")
	d.setReader(in)
	assert.Equal(t, in, d.in)
	out := &bytes.Buffer{}
	d.setWriter(out)
	assert.Equal(t, out, d.out)
}

func TestDropdownSetWriterUsesTuiViewport(t *testing.T) {
	ctx := t.Context()
	cio := newUnstartedIO(ctx, 10, 2, 0)
	tui := &Tui{ctx: ctx, termIO: &termIO{out: cio}}
	d := newDropdown()
	d.setWriter(tui)
	_, ok := d.out.(*viewport)
	assert.True(t, ok)
}

func TestDropdownPressKeyRuneEnter(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"a", "b"}
	d.relevant = []int{0, 1}
	d.displayed = []int{0, 1}
	d.selected = 1
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	io := newTestTermIO(10, 4)
	assert.Equal(t, 1, d.pressKeyRune(io, keyEnter, len(d.displayed), 4))
	assert.Equal(t, 1, len(events))
	confirmed, ok := events[0].(dropdownConfirmed)
	assert.True(t, ok)
	assert.Equal(t, 1, confirmed.Selected)
}

func TestDropdownPressKeyRuneBackspace(t *testing.T) {
	d := newDropdown()
	d.trie = newTrie()
	d.trie.Add("a", 0)
	d.relevant = []int{0}
	d.displayed = []int{0}
	d.Items = []any{"a"}
	d.typed = []rune("a")
	io := newTestTermIO(10, 4)
	d.pressKeyRune(io, 0x7f, len(d.displayed), 4)
	assert.Equal(t, 0, len(d.typed))
}

type errWriterDropdown struct{}

func (errWriterDropdown) Write(p []byte) (int, error) {
	return 0, io.EOF
}

func TestDropdownParseTemplatesError(t *testing.T) {
	d := newDropdown()
	d.LabelTemplate = "{{"
	assert.Error(t, d.parseTemplates())
}

func TestDropdownParseTemplatesLabelExecuteError(t *testing.T) {
	d := newDropdown()
	d.LabelTemplate = "{{ call . }}"
	assert.Error(t, d.parseTemplates())
}

func TestDropdownParseTemplatesActiveError(t *testing.T) {
	d := newDropdown()
	d.LabelTemplate = "{{.}} "
	d.ActiveItemTemplate = "{{"
	assert.Error(t, d.parseTemplates())
}

func TestDropdownShowAnswerWriteError(t *testing.T) {
	d := newDropdown()
	d.out = errWriterDropdown{}
	assert.NoError(t, d.parseTemplates())
	assert.Error(t, d.showAnswer("Label", "Value"))
}

func TestDropdownPressKeyRuneArrows(t *testing.T) {
	d := newDropdown()
	d.relevant = []int{0, 1}
	d.displayed = []int{0, 1}
	d.selected = 0
	io := newTestTermIO(10, 4)
	d.pressKeyRune(io, '↓', len(d.displayed), 4)
	assert.Equal(t, 1, d.selected)
	d.pressKeyRune(io, '↑', len(d.displayed), 4)
	assert.Equal(t, 0, d.selected)
}

func TestDropdownRunMainConsumesInputChannel(t *testing.T) {
	d := newDropdown()
	d.trie = newTrie()
	d.trie.Add("a", 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	input := make(chan dropdownInputEvent, 1)
	input <- dropdownInputConfirmed{Index: 0}
	d.input = input
	io := newTestTermIO(10, 4)
	frame := bytes.NewBuffer(nil)
	i, err := d.runMain(io, frame, 1, 1)
	assert.NoError(t, err)
	assert.Equal(t, 0, i)
}

func TestDropdownRunMainRejectsInvalidConfirmedIndex(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	input := make(chan dropdownInputEvent, 1)
	input <- dropdownInputConfirmed{Index: 5}
	d.input = input
	io := newTestTermIO(10, 4)
	frame := bytes.NewBuffer(nil)
	i, err := d.runMain(io, frame, 1, 1)
	assert.NoError(t, err)
	assert.Equal(t, -1, i)
}

func TestDropdownPressKeyRuneDefault(t *testing.T) {
	d := newDropdown()
	d.trie = newTrie()
	d.trie.Add("a", 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	io := newTestTermIO(10, 4)
	assert.Equal(t, -1, d.pressKeyRune(io, 'a', len(d.displayed), 4))
	assert.Equal(t, "a", string(d.typed))
}

func TestDropdownPressKeyRuneEmitsStateEvents(t *testing.T) {
	d := newDropdown()
	d.trie = newTrie()
	d.trie.Add("alpha", 0)
	d.trie.Add("beta", 1)
	d.Items = []any{"alpha", "beta"}
	d.relevant = []int{0, 1}
	d.displayed = []int{0, 1}
	d.selected = 1
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	io := newTestTermIO(10, 4)
	assert.Equal(t, -1, d.pressKeyRune(io, 'a', len(d.displayed), 4))
	assert.Equal(t, 1, len(events))
	filtered, ok := events[0].(dropdownFilterChanged)
	assert.True(t, ok)
	assert.Equal(t, "a", filtered.Prefix)
	assert.Equal(t, 1, filtered.Matching)
	assert.Equal(t, 0, len(filtered.Added))
	assert.Equal(t, 1, len(filtered.Removed))
	assert.Equal(t, 1, filtered.Removed[0])
}

func TestDropdownPressKeyRuneOneReturn(t *testing.T) {
	d := newDropdown()
	d.OneReturn = true
	d.trie = newTrie()
	d.trie.Add("a", 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	io := newTestTermIO(10, 4)
	assert.Equal(t, 0, d.pressKeyRune(io, 'a', len(d.displayed), 4))
	assert.Equal(t, 2, len(events))
	_, ok := events[0].(dropdownFilterChanged)
	assert.True(t, ok)
	confirmed, ok := events[1].(dropdownConfirmed)
	assert.True(t, ok)
	assert.Equal(t, 0, confirmed.Selected)
}

func TestDropdownHandleLazyItemEmpty(t *testing.T) {
	d := newDropdown()
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{}, false)
	assert.ErrorIs(t, err, ErrEmptyLazyResult)
}

func TestDropdownHandleLazyItemError(t *testing.T) {
	d := newDropdown()
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{err: io.EOF}, true)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownHandleLazyItemAdds(t *testing.T) {
	d := newDropdown()
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, needsRender, err := d.handleLazyItem(tio, frame, 1, itPair{item: "item"}, true)
	assert.NoError(t, err)
	assert.True(t, needsRender)
}

func TestDropdownHandleLazyItemOneReturnWhenDone(t *testing.T) {
	d := newDropdown()
	d.OneReturn = true
	d.Items = []any{"one"}
	d.relevant = []int{0}
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	i, needsRender, err := d.handleLazyItem(tio, frame, 1, itPair{}, false)
	assert.NoError(t, err)
	assert.Equal(t, 0, i)
	assert.True(t, !needsRender)
	assert.Equal(t, 1, len(events))
	confirmed, ok := events[0].(dropdownConfirmed)
	assert.True(t, ok)
	assert.Equal(t, 0, confirmed.Selected)
}

func TestDropdownAddItemPreservesFilter(t *testing.T) {
	d := newDropdown()
	d.trie = newTrie()
	assert.NoError(t, d.parseTemplates())
	assert.NoError(t, d.addItem(6, "alpha"))
	assert.True(t, !d.filterWith("a", len(d.displayed), 6, 3))
	assert.Equal(t, []int{0}, d.relevant)
	assert.NoError(t, d.addItem(6, "beta"))
	assert.Equal(t, []int{0}, d.relevant)
	assert.Equal(t, []int{0}, d.displayed)
}

func TestDropdownHandleLazyKeyEOF(t *testing.T) {
	d := newDropdown()
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyKey(tio, frame, 1, 1, keyEvent{}, false)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownHandleLazyKeyPasteIgnored(t *testing.T) {
	d := newDropdown()
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyKey(tio, frame, 1, 1, keyEvent{err: &pasteTextError{buf: []byte("a")}}, true)
	assert.NoError(t, err)
}

func TestDropdownHandleLazyKeyEnter(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"one"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	i, _, err := d.handleLazyKey(tio, frame, 1, 1, keyEvent{key: keyEnter}, true)
	assert.NoError(t, err)
	assert.Equal(t, 0, i)
}

func TestDropdownRunRenderLoadsItem(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"seed"}
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	ch := make(chan itPair, 1)
	ch <- itPair{item: "next"}
	close(ch)
	d.itItems = ch
	_, err := d.runRender(tio, frame)
	assert.NoError(t, err)
}

func TestDropdownLoadItemDone(t *testing.T) {
	d := newDropdown()
	tio := newTestTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	d.itItems = make(chan itPair)
	err := d.loadItem(tio, frame, itPair{}, false, 1)
	assert.NoError(t, err)
	assert.True(t, d.itItems == nil)
	assert.True(t, d.iterDone)
}

func TestDropdownAddItemEmitsAppendEvent(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"seed"}
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 2)
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
	d := newDropdown()
	tio := newTestTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	err := d.loadItem(tio, frame, itPair{err: io.EOF}, true, 1)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownLoadItemWriteError(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"seed"}
	assert.NoError(t, d.parseTemplates())
	initIO := newTestTermIO(20, 6)
	_, err := d.renderInit(initIO)
	assert.NoError(t, err)
	tio := &termIO{
		in:      bytes.NewBuffer(nil),
		out:     errWriterDropdown{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	err = d.loadItem(tio, frame, itPair{item: "next"}, true, 1)
	assert.Error(t, err)
}

func TestDropdownRenderLazyFrameClears(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"item"}
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	space := 1
	displayed := 0
	err := d.renderLazyFrame(tio, frame, &space, &displayed)
	assert.NoError(t, err)
	assert.True(t, space > 0)
	assert.True(t, displayed > 0)
}

func TestDropdownRenderLazyFrameWriteError(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"item"}
	assert.NoError(t, d.parseTemplates())
	tio := &termIO{
		in:      bytes.NewBuffer(nil),
		out:     errWriterDropdown{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	space := 0
	displayed := 0
	err := d.renderLazyFrame(tio, frame, &space, &displayed)
	assert.Error(t, err)
}

func TestDropdownRunRenderWriteError(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"item"}
	assert.NoError(t, d.parseTemplates())
	tio := &termIO{
		in:      bytes.NewBuffer([]byte{keyEnter}),
		out:     errWriterDropdown{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runRender(tio, frame)
	assert.Error(t, err)
}

func TestDropdownHandleLazyKeyReadError(t *testing.T) {
	d := newDropdown()
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyKey(tio, frame, 1, 1, keyEvent{err: io.EOF}, true)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownRunRenderUsesMain(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"item"}
	assert.NoError(t, d.parseTemplates())
	tio := &termIO{
		in:      bytes.NewBuffer([]byte{keyEnter}),
		out:     &bytes.Buffer{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	i, err := d.runRender(tio, frame)
	assert.NoError(t, err)
	assert.True(t, i >= 0)
}

func TestDropdownRunMainPasteIgnored(t *testing.T) {
	d := newDropdown()
	tio := &termIO{
		in:      bytes.NewBufferString("ab"),
		out:     &bytes.Buffer{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runMain(tio, frame, 1, 1)
	assert.NoError(t, err)
}

func TestDropdownRunMainReadError(t *testing.T) {
	d := newDropdown()
	tio := &termIO{
		in:      bytes.NewBuffer(nil),
		out:     &bytes.Buffer{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runMain(tio, frame, 1, 1)
	assert.Error(t, err)
}

func TestDropdownRunRenderContextDone(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"item"}
	assert.NoError(t, d.parseTemplates())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d.Ctx = ctx
	tio := &termIO{
		in:      bytes.NewBuffer([]byte{keyEnter}),
		out:     &bytes.Buffer{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runRender(tio, frame)
	assert.Error(t, err)
}

func TestDropdownNextLazyActionItem(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"seed"}
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	_, err := d.renderInit(tio)
	assert.NoError(t, err)
	frame := bytes.NewBuffer(nil)
	d.itItems = make(chan itPair, 1)
	d.itItems <- itPair{item: "next"}
	res, err := d.nextLazyAction(tio, frame, 1, 1, make(chan keyEvent))
	assert.NoError(t, err)
	assert.True(t, res.needsRender)
}

func TestDropdownNextLazyActionKey(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"seed"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	keys := make(chan keyEvent, 1)
	keys <- keyEvent{key: keyEnter}
	res, err := d.nextLazyAction(tio, frame, 1, 1, keys)
	assert.NoError(t, err)
	assert.True(t, res.done)
	assert.Equal(t, 0, res.index)
}

func TestDropdownNextLazyActionContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d := newDropdown()
	d.Ctx = ctx
	tio := newTestTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	_, err := d.nextLazyAction(tio, frame, 1, 1, make(chan keyEvent))
	assert.Error(t, err)
}

func TestDropdownHandleLazyItemDoneWithItems(t *testing.T) {
	d := newDropdown()
	d.Items = []any{"item"}
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{}, false)
	assert.NoError(t, err)
	assert.True(t, d.iterDone)
}

func TestDropdownHandleLazyItemAddError(t *testing.T) {
	d := newDropdown()
	d.activeItemTemplate = template.Must(template.New("active").Parse("{{ . }}"))
	d.inactiveItemTemplate = template.Must(template.New("inactive").Parse("{{call .}}"))
	d.trie = newTrie()
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{item: "x"}, true)
	assert.Error(t, err)
}

func TestDropdownAddItemDeduplicatesLazyRelevantIndexes(t *testing.T) {
	d := newDropdown()
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	_, err := d.renderInit(tio)
	assert.NoError(t, err)
	err = d.addItem(tio.Height, "alpha beta")
	assert.NoError(t, err)
	assert.Equal(t, []int{0}, d.relevant)
	assert.Equal(t, []int{0}, d.displayed)
}

func captureOutput(cio *chanIO) <-chan string {
	out := make(chan string, 32)
	go func() {
		for line := range cio.Out {
			out <- line
		}
		close(out)
	}()
	return out
}

// itemsLoadedWriter signals once the dropdown renders a frame with items.
// Writes happen on the dropdown goroutine, so reading Items here is race-free.
type itemsLoadedWriter struct {
	io.Writer
	d     *dropdown
	ready chan struct{}
	once  *sync.Once
}

func (w itemsLoadedWriter) Write(p []byte) (int, error) {
	if len(w.d.Items) > 0 {
		w.once.Do(func() { close(w.ready) })
	}
	return w.Writer.Write(p)
}

// notifyItemsLoaded must be applied after WithOutput.
func notifyItemsLoaded() (opt, <-chan struct{}) {
	ready := make(chan struct{})
	once := &sync.Once{}
	return opT(func(d *dropdown) error {
		d.out = itemsLoadedWriter{Writer: d.out, d: d, ready: ready, once: once}
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

func lazySeq(values ...string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for _, v := range values {
			if !yield(v, nil) {
				return
			}
		}
	}
}

func TestDropdownLazySelectsItem(t *testing.T) {
	cio, opts := testIOforDropdown(t, 20, 6)
	itemsLoaded, ready := notifyItemsLoaded()
	opts = WithOptions(opts, itemsLoaded)
	out := captureOutput(cio)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy("Select", lazySeq("red", "green", "blue"), opts)
		resCh <- struct {
			value string
			err   error
		}{value, err}
	}()
	waitForItems(t, ready)
	cio.In <- "\x0d" // enter
	final := waitForOutputContaining(t, out, "Select: red", 2*time.Second)
	res := <-resCh
	assert.NoError(t, res.err)
	assert.Equal(t, "red", res.value)
	assert.Contains(t, final, "Select: red")
}

func TestDropdownLazyDoesNotLeaveBlockedReaderAfterConfirm(t *testing.T) {
	in := newBlockingByteReader()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		in.Close()
	})
	itemsLoaded, ready := notifyItemsLoaded()
	opts := WithOptions(
		WithInput(in),
		WithOutput(io.Discard),
		WithContext(ctx),
		opT(func(d *dropdown) error {
			d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   20,
					Height:  6,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
		itemsLoaded,
	)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy("Select", lazySeq("red", "green"), opts)
		resCh <- struct {
			value string
			err   error
		}{value, err}
	}()
	waitForItems(t, ready)
	waiterDeadline := time.Now().Add(2 * time.Second)
	for in.Waiters() == 0 {
		if time.Now().After(waiterDeadline) {
			t.Fatal("timed out waiting for dropdown reader")
		}
		time.Sleep(5 * time.Millisecond)
	}
	in.SendByte(byte(keyEnter))
	res := <-resCh
	assert.NoError(t, res.err)
	assert.Equal(t, "red", res.value)
	assertNoWaitersFor(t, in, 200*time.Millisecond)
}

func TestDropdownLazyDoesNotStartProducerWhenContextAlreadyCanceled(t *testing.T) {
	in := newBlockingByteReader()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	t.Cleanup(in.Close)
	var started atomic.Bool
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
	})
	opts := WithOptions(
		WithInput(in),
		WithOutput(io.Discard),
		WithContext(ctx),
		opT(func(d *dropdown) error {
			d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   20,
					Height:  6,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
	)
	_, err := DropdownLazy("Select", func(yield func(string, error) bool) {
		started.Store(true)
		<-release
	}, opts)
	assert.Error(t, err)
	// keep polling briefly so delayed scheduling still gets caught.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if started.Load() {
			t.Fatal("lazy producer started despite canceled context")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDropdownLazyCtrlCNotLostAfterPreviousLazyExit(t *testing.T) {
	in, writer, err := os.Pipe()
	assert.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		if closeErr := in.Close(); closeErr != nil {
			t.Errorf("close reader: %v", closeErr)
		}
		if closeErr := writer.Close(); closeErr != nil {
			t.Errorf("close writer: %v", closeErr)
		}
	})
	itemsLoaded, ready := notifyItemsLoaded()
	opts := WithOptions(
		WithInput(in),
		WithOutput(io.Discard),
		WithContext(ctx),
		opT(func(d *dropdown) error {
			d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   20,
					Height:  6,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
		itemsLoaded,
	)
	_, err = DropdownLazy("First", func(yield func(string, error) bool) {
		yield("", io.EOF)
	}, opts)
	assert.Error(t, err)
	resCh := make(chan error, 1)
	go func() {
		_, err := DropdownLazy("Second", lazySeq("a", "b"), opts)
		resCh <- err
	}()
	waitForItems(t, ready)
	_, err = writer.Write([]byte{keyCtrlC})
	assert.NoError(t, err)
	select {
	case err = <-resCh:
		assert.ErrorIs(t, err, io.EOF)
	case <-time.After(2 * time.Second):
		t.Fatal("second dropdown did not receive Ctrl+C")
	}
}

func TestDropdownLazyArrowAndEnterWorkAfterPreviousLazyExit(t *testing.T) {
	in, writer, err := os.Pipe()
	assert.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		if closeErr := in.Close(); closeErr != nil {
			t.Errorf("close reader: %v", closeErr)
		}
		if closeErr := writer.Close(); closeErr != nil {
			t.Errorf("close writer: %v", closeErr)
		}
	})
	itemsLoaded, ready := notifyItemsLoaded()
	opts := WithOptions(
		WithInput(in),
		WithOutput(io.Discard),
		WithContext(ctx),
		opT(func(d *dropdown) error {
			d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   20,
					Height:  6,
					Restore: func() error { return nil },
				}, nil
			}
			return nil
		}),
		itemsLoaded,
	)
	_, err = DropdownLazy("First", func(yield func(string, error) bool) {
		yield("", io.EOF)
	}, opts)
	assert.Error(t, err)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy("Second", lazySeq("a", "b"), opts)
		resCh <- struct {
			value string
			err   error
		}{value, err}
	}()
	waitForItems(t, ready)
	_, err = writer.Write([]byte{0x1b, 0x5b, 0x42}) // down arrow
	assert.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	_, err = writer.Write([]byte{keyEnter})
	assert.NoError(t, err)
	select {
	case res := <-resCh:
		assert.NoError(t, res.err)
		assert.Equal(t, "b", res.value)
	case <-time.After(2 * time.Second):
		t.Fatal("second dropdown did not process down+enter")
	}
}

func TestWithOneMatch_setsValue(t *testing.T) {
	d := newDropdown()
	err := WithDefault("alpha")(d)
	assert.NoError(t, err)
	assert.Equal(t, "alpha", d.oneMatch)
}

func TestWithOneMatch_singlePrefixMatchReturnsIndex(t *testing.T) {
	d := newDropdown()
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 2, Name: "beta"},
	}
	err := WithDefault("beta")(d)
	assert.NoError(t, err)
	assert.NoError(t, d.parseTemplates())
	_, err = d.renderInit(newTestTermIO(20, 6))
	var oneMatch oneHatch
	ok := errors.As(err, &oneMatch)
	assert.True(t, ok)
	assert.Equal(t, 1, int(oneMatch))
	assert.Equal(t, "", d.oneMatch)
}

func TestWithOneMatch_noMatchKeepsDropdown(t *testing.T) {
	d := newDropdown()
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 2, Name: "beta"},
	}
	err := WithDefault("gamma")(d)
	assert.NoError(t, err)
	assert.NoError(t, d.parseTemplates())
	_, err = d.renderInit(newTestTermIO(20, 6))
	assert.NoError(t, err)
	assert.Equal(t, 2, len(d.Items))
	assert.Equal(t, "", d.oneMatch)
}

func TestWithOneMatch_sortsByLevensteinDistance(t *testing.T) {
	d := newDropdown()
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "zeta"},
		dropdownHeuristicLabelItem{ID: 2, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 3, Name: "omega"},
	}
	err := WithDefault("alpa")(d)
	assert.NoError(t, err)
	assert.NoError(t, d.parseTemplates())
	_, err = d.renderInit(newTestTermIO(20, 6))
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 0, 2}, d.relevant)
	assert.Equal(t, []int{1, 0, 2}, d.displayed)
	assert.Equal(t, "", d.oneMatch)
}

func TestWithOneMatch_multipleMatchesKeepDropdown(t *testing.T) {
	d := newDropdown()
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "same"},
		dropdownHeuristicLabelItem{ID: 2, Name: "same"},
	}
	err := WithDefault("same")(d)
	assert.NoError(t, err)
	assert.NoError(t, d.parseTemplates())
	_, err = d.renderInit(newTestTermIO(20, 6))
	assert.NoError(t, err)
	assert.Equal(t, 2, len(d.Items))
	assert.Equal(t, "", d.oneMatch)
}

func TestDropdownLevenstein(t *testing.T) {
	d := newDropdown()
	assert.Equal(t, 3, d.levenstein("kitten", "sitting"))
	assert.Equal(t, 0, d.levenstein("alpha", "alpha"))
	assert.Equal(t, 1, d.levenstein("alpa", "alpha"))
}

func TestDropdownLazyEmptySequence(t *testing.T) {
	cio, opts := testIOforDropdown(t, 20, 6)
	captureOutput(cio)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy("Empty", func(yield func(string, error) bool) {}, opts)
		resCh <- struct {
			value string
			err   error
		}{value, err}
	}()
	res := <-resCh
	assert.ErrorIs(t, res.err, ErrEmptyLazyResult)
	assert.Equal(t, "", res.value)
}

func TestDropdownLazyOneReturnSingleItem(t *testing.T) {
	cio, opts := testIOforDropdown(t, 20, 6, WithOneReturn())
	captureOutput(cio)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy("Single", lazySeq("only"), opts)
		resCh <- struct {
			value string
			err   error
		}{value, err}
	}()
	select {
	case res := <-resCh:
		assert.NoError(t, res.err)
		assert.Equal(t, "only", res.value)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for single-item lazy OneReturn")
	}
}
