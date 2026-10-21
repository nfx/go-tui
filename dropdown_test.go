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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestDropdownPressKeyCancelledReadKeepsNextInput(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	in := &notifyingReader{File: pr, started: make(chan struct{})}
	d := newDropdown(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d.Ctx = ctx
	tio := &termIO{in: in, out: &bytes.Buffer{}}
	finished := make(chan error, 1)
	go func() {
		_, err := d.pressKey(d.Ctx, tio, &bytes.Buffer{}, 1, 1)
		finished <- err
	}()
	select {
	case <-in.started:
	case <-time.After(time.Second):
		t.Fatal("dropdown did not start reading")
	}
	cancel()
	select {
	case err := <-finished:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled dropdown did not return")
	}
	_, err = pw.Write([]byte{'x', keyEnter})
	assert.NoError(t, err)
	next := newInput(t.Context(), "next")
	next.ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	next.in, next.out = in, &bytes.Buffer{}
	next.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 20, Height: 2, Restore: func() error { return nil }}, nil
	}
	assert.NoError(t, next.parseTemplates())
	got, err := next.run(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
}

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
		result <- Confirm(cio.ctx, "Are you sure?", opts)
	}()
	return cio.In, cio.Out, result
}

func TestSimpleCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t, "\rAre you sure? \n\r│ + Yes\n\r│ - No\n\r│ up/down move · type filter\n\r", <-out)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "Are you sure?: Yes\n", waitForDropdownOutput(t, out, "Are you sure?: Yes\n"))
	assert.Equal(t, true, <-res)
}

func TestDenyCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t, "\rAre you sure? \n\r│ + Yes\n\r│ - No\n\r│ up/down move · type filter\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rAre you sure? \n\r│ - Yes\n\r│ + No\n\r│ up/down move · type filter\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "Are you sure?: No\n", waitForDropdownOutput(t, out, "Are you sure?: No\n"))
	assert.Equal(t, false, <-res)
}

func TestDownAndUpCase(t *testing.T) {
	in, out, res := confirmForTest(t)
	assert.Equal(t, "\rAre you sure? \n\r│ + Yes\n\r│ - No\n\r│ up/down move · type filter\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	<-out                // frame render
	in <- "\x1b\x5b\x41" // up
	<-out                // frame render
	in <- "\x0d"         // enter
	<-out                // clear
	assert.Equal(t, "Are you sure?: Yes\n", waitForDropdownOutput(t, out, "Are you sure?: Yes\n"))
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
		v, err := Dropdown(cio.ctx, "Pick letter", []string{
			"A", "B", "C", "D", "E",
		}, opts)
		result <- picked{v, err}
	}()
	return cio.In, cio.Out, result
}

func TestMoreItems(t *testing.T) {
	in, out, res := overflowForTest(t)
	assert.Equal(t, "\rPick letter \n\r│ + A\n\r│ - B\n\r│ ~ 3 of 5 more\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rPick letter \n\r│ + B\n\r│ - C\n\r│ ~ 2 of 5 more\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rPick letter \n\r│ + C\n\r│ - D\n\r│ ~ 1 of 5 more\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	// the key hint takes the row of the count
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rPick letter \n\r│ + D\n\r│ - E\n\r│ up/down move · type filter\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rPick letter \n\r│ - D\n\r│ + E\n\r│ up/down move · type filter\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down, no more items, might bell
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rPick letter \n\r│ - D\n\r│ + E\n\r│ up/down move · type filter\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x41" // up
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rPick letter \n\r│ + D\n\r│ - E\n\r│ up/down move · type filter\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "Pick letter: D\n", waitForDropdownOutput(t, out, "Pick letter: D\n"))
	assertPicked(t, "D", res)
}

func TestMoreItemsUp(t *testing.T) {
	in, out, res := overflowForTest(t)
	assert.Equal(t, "\rPick letter \n\r│ + A\n\r│ - B\n\r│ ~ 3 of 5 more\n\r", <-out)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rPick letter \n\r│ + B\n\r│ - C\n\r│ ~ 2 of 5 more\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x41" // up
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rPick letter \n\r│ + A\n\r│ - B\n\r│ ~ 3 of 5 more\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(t, "Pick letter: A\n", waitForDropdownOutput(t, out, "Pick letter: A\n"))
	assertPicked(t, "A", res)
}

// otherDropdownForTest starts a dropdown for filtering and clipping checks.
func otherDropdownForTest(t *testing.T) (in, out chan string, result chan picked) {
	t.Helper()
	// room for the label, two items and the count, as in overflowForTest
	cio, opts := testIOforDropdown(t, 30, 5)
	result = make(chan picked, 1)
	go func() {
		v, err := Dropdown(cio.ctx, "Neque porro", []string{
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
	assert.Equal(t, "\rNeque porro \n\r│ + Lorem ipsum\n\r│ - dolor sit amet\n\r│ ~ 3 of 5 more\n\r", <-out)
	in <- "c"
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rNeque porro \n\r│ + condimentum libero\n\r│ up/down move · type filter\n\r",
		<-out,
	)
	in <- "\x7f" // backspace
	assert.Equal(
		t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r"+"\rNeque porro \n\r│ + Lorem ipsum\n\r│ - dolor sit amet\n\r│ ~ 3 of 5 more\n\r",
		<-out,
	)
	in <- "l"
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rNeque porro \n\r│ + Lorem ipsum\n\r│ - condimentum libero\n\r│ up/down move · type filter\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	assert.Equal(
		t,
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+"\rNeque porro \n\r│ - Lorem ipsum\n\r│ + condimentum libero\n\r│ up/down move · type filter\n\r",
		<-out,
	)
	in <- "\x0d" // enter
	assert.Equal(t, "\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r", <-out)
	assert.Equal(
		t,
		"Neque porro: condimentum libero\n",
		waitForDropdownOutput(t, out, "Neque porro: condimentum libero\n"),
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
		key, value, err := DropdownKV(cio.ctx, "Select item", items, opts)
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
	assert.Equal(t, "\rSelect item \n\r│ + {apple 42}\n\r│ up/down move · type filter\n\r", <-out)
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
	assert.Equal(
		t,
		"\rSelect item \n\r│ + {apple 2}\n\r│ - {banana 3}\n\r│ - {zebra 1}\n\r│ up/down move · type filter\n\r",
		<-out,
	)
	in <- "\x1b\x5b\x42" // down
	clear5 := "\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"
	assert.Equal(t,
		clear5+"\rSelect item \n\r│ - {apple 2}\n\r│ + {banana 3}\n\r│ - {zebra 1}\n\r│ up/down move · type filter\n\r",
		<-out)
	in <- "\x0d" // enter
	assert.Equal(t, clear5, <-out)
	assert.Equal(t, "Select item: {banana 3}\n", waitForDropdownOutput(t, out, "Select item: {banana 3}\n"))
	result := <-res
	assert.Equal(t, "banana", result.key)
	assert.Equal(t, 3, result.value)
	assert.NoError(t, result.err)
}

func TestDropdownLazyClearsOnlyRenderedArea(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
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
	idx, err := d.dropdownIndex(t.Context())
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
	d := newDropdown(t.Context())
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
	d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
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
	reader := &chunkReader{chunks: [][]byte{
		{'b'},
	}}
	d := newDropdown(t.Context())
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
	idx, err := d.dropdownIndex(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestDropdownPressKeyRuneAndRenderMore(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"alpha", "beta"}
	d.relevant = []int{0, 1}
	d.displayed = d.relevant
	d.trie = newTrie()
	d.trie.Add(text("alpha"), 0)
	d.trie.Add(text("beta"), 1)

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
	d.trie.Add(text("abc"), 0)
	d.Items = []any{"abc"}
	d.relevant = []int{0}
	d.displayed = d.relevant
	assert.True(t, d.pressAny('a', len(d.displayed), 4))

	var frame bytes.Buffer
	assert.NoError(t, d.clearFrame(tio, &frame, 0))
}

func TestDropdownRenderInitAndItems(t *testing.T) {
	d := newDropdown(t.Context())
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

	assert.True(t, len(d.active[d.displayed[0]]) > 0)
}

func TestDropdownRenderInitEmitsEvents(t *testing.T) {
	d := newDropdown(t.Context())
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

func TestDropdownPressKeys(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	io := newTestTermIO(10, 4)
	frame := &bytes.Buffer{}
	assert.NoError(t, d.clearFrame(io, frame, 2))
	buf, ok := io.out.(*bytes.Buffer)
	assert.True(t, ok)
	assert.True(t, buf.Len() > 0)
}

func TestDropdownOptionHelpers(t *testing.T) {
	d := newDropdown(t.Context())

	for _, opt := range []opt{
		WithOneReturn(),
		WithHide(),
		WithTemplate(".Label"),
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

// bindDropdown binds templates and prompt chrome for tests that bypass run.
func bindDropdown(t *testing.T, d *dropdown) {
	t.Helper()
	withEnv(t)
	if d.out == nil {
		d.out = &bytes.Buffer{}
	}
	tt := &termIO{out: d.out, Width: 40, Height: 10}
	assert.NoError(t, d.parseTemplates())
	assert.NoError(t, d.bind(defaultIO.bindTheme(t.Context(), d.theme, d.out, tt)))
	assert.NoError(t, d.renderChrome())
}

func TestShowAnswerWritesWhenVisible(t *testing.T) {
	d := newDropdown(t.Context())
	d.Label, d.Items = "Test", []any{"value"}
	out := &bytes.Buffer{}
	d.out = out
	bindDropdown(t, d)
	assert.NoError(t, d.answer("Test", 0))
	assert.Equal(t, "✔ Test · value\n", out.String())
}

func TestDropdownPressAnyOneReturn(t *testing.T) {
	d := newDropdown(t.Context())
	d.OneReturn = true
	d.trie = newTrie()
	d.trie.Add(text("ok"), 0)

	assert.True(t, d.pressAny('o', 1, 1))
}

func TestConfirmfRunsWithFormattedAction(t *testing.T) {
	orig := confirmRunner
	defer func() { confirmRunner = orig }()
	confirmRunner = func(ctx context.Context, action string, opts ...opt) bool {
		assert.Equal(t, "Proceed with task?", action)
		return true
	}

	assert.True(t, Confirmf(t.Context(), "Proceed with %s?", "task"))
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
	assert.True(t, defaultConfirmRunner(t.Context(), "Proceed?", WithInput(in), WithOutput(out), opt))
}

func TestDefaultConfirmRunnerReturnsFalseOnError(t *testing.T) {
	opt := opT(func(d *dropdown) error {
		d.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) {
			return nil, io.EOF
		}

		return nil
	})
	assert.True(t, !defaultConfirmRunner(t.Context(), "Proceed?", opt))
}

func TestWithTemplateSetsTemplates(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithTemplate(".Name")(d))
	// a projection, which leaves the presentation to the theme
	assert.Equal(t, `{{ __content .Name }}`, d.mainSource)
	assert.Equal(t, "", d.detailsSource)
	assert.True(t, !d.activeSet && !d.inactiveSet && !d.answerSet)
}

func TestWithTemplateAddsActiveDetails(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithTemplate(".Name", ".Type", ".Owner")(d))
	assert.Equal(t, `{{ __content (.Type) ", " (.Owner) }}`, d.detailsSource)
}

func TestDropdownItemLabelUsesHeuristicField(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownHeuristicLabelItem{ID: 42, Name: "alpha"}
	assert.Equal(t, "alpha", d.itemLabel(item))
}

func TestDropdownItemLabelUsesDisplayNameHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownDisplayNameLabelItem{DisplayName: "service-a"}
	assert.Equal(t, "service-a", d.itemLabel(item))
}

func TestDropdownItemLabelUsesFullNameHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownFullNameLabelItem{FullName: "Example Service"}
	assert.Equal(t, "Example Service", d.itemLabel(item))
}

func TestDropdownItemLabelUsesSummaryHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownSummaryLabelItem{Summary: "concise summary"}
	assert.Equal(t, "concise summary", d.itemLabel(item))
}

func TestDropdownItemLabelUsesTextHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownTextLabelItem{Text: "plaintext value"}
	assert.Equal(t, "plaintext value", d.itemLabel(item))
}

func TestDropdownItemLabelUsesSubjectHeuristic(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownSubjectLabelItem{Subject: "subject line"}
	assert.Equal(t, "subject line", d.itemLabel(item))
}

func TestDropdownItemLabelUsesAnnotation(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownAnnotatedLabelItem{
		Name:        "fallback",
		Description: "annotated",
	}
	assert.Equal(t, "annotated", d.itemLabel(item))
}

func TestDropdownItemLabelFallsBackToStringer(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownStringerLabelItem{ID: 7}
	assert.Equal(t, "stringer-7", d.itemLabel(item))
}

func TestDropdownItemLabelFallsBackToPointerStringer(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownPointerStringerLabelItem{ID: 11}
	assert.Equal(t, "ptr-stringer-11", d.itemLabel(item))
}

func TestDropdownSetItemUsesResolvedLabelForTrie(t *testing.T) {
	d := newDropdown(t.Context())
	item := dropdownHeuristicLabelItem{ID: 3, Name: "omega"}
	d.active = make([]text, 1)
	d.activeWidths = make([]int, 1)
	d.inactive = make([]text, 1)
	d.widths = make([]int, 1)
	d.content = make([]text, 1)
	d.search = make([]string, 1)
	d.relevant = make([]int, 1)
	d.trie = newTrie()
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
	assert.NoError(t, d.answer("Pick", 0))
	assert.Contains(t, out.String(), "delta")
	assert.NotContains(t, out.String(), "{")
}

func TestDropdownContextAndIOSetters(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	d.setWriter(tui)
	_, ok := d.out.(*viewport)
	assert.True(t, ok)
}

func TestDropdownPressKeyRuneEnter(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	d.trie = newTrie()
	d.trie.Add(text("a"), 0)
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
	d := newDropdown(t.Context())
	assert.NoError(t, WithLabelTemplate("{{")(d))
	assert.Error(t, d.parseTemplates())
}

func TestDropdownParseTemplatesLabelExecuteError(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithLabelTemplate("{{ call . }}")(d))
	assert.NoError(t, d.parseTemplates())
	// a per-call label runs when the dropdown binds
	assert.NoError(t, d.bind(defaultIO.bindTheme(t.Context(), d.theme, &bytes.Buffer{}, nil)))
	assert.Error(t, d.renderChrome())
}

func TestDropdownParseTemplatesActiveError(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithActiveItemTemplate("{{")(d))
	assert.Error(t, d.parseTemplates())
}

func TestDropdownShowAnswerWriteError(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"Value"}
	bindDropdown(t, d)
	d.out = errWriterDropdown{}
	assert.Error(t, d.answer("Label", 0))
}

func TestDropdownPressKeyRuneArrows(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	d.trie = newTrie()
	d.trie.Add(text("a"), 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	input := make(chan dropdownInputEvent, 1)
	input <- dropdownInputConfirmed{Index: 0}
	d.input = input
	io := newTestTermIO(10, 4)
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
	io := newTestTermIO(10, 4)
	frame := bytes.NewBuffer(nil)
	i, err := d.runMain(d.Ctx, io, frame, 1, 1)
	assert.NoError(t, err)
	assert.Equal(t, -1, i)
}

func TestDropdownPressKeyRuneDefault(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = newTrie()
	d.trie.Add(text("a"), 0)
	d.Items = []any{"a"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	io := newTestTermIO(10, 4)
	assert.Equal(t, -1, d.pressKeyRune(io, 'a', len(d.displayed), 4))
	assert.Equal(t, "a", string(d.typed))
}

func TestDropdownPressKeyRuneEmitsStateEvents(t *testing.T) {
	d := newDropdown(t.Context())
	d.trie = newTrie()
	d.trie.Add(text("alpha"), 0)
	d.trie.Add(text("beta"), 1)
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
	d := newDropdown(t.Context())
	d.OneReturn = true
	d.trie = newTrie()
	d.trie.Add(text("a"), 0)
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
	d := newDropdown(t.Context())
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{}, false)
	assert.ErrorIs(t, err, ErrEmptyLazyResult)
}

func TestDropdownHandleLazyItemError(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{err: io.EOF}, true)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownHandleLazyItemAdds(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, needsRender, err := d.handleLazyItem(tio, frame, 1, itPair{item: "item"}, true)
	assert.NoError(t, err)
	assert.True(t, needsRender)
}

func TestDropdownHandleLazyItemOneReturnWhenDone(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	d.trie = newTrie()
	bindDropdown(t, d)
	assert.NoError(t, d.addItem(6, "alpha"))
	assert.True(t, !d.filterWith("a", len(d.displayed), 6, 3))
	assert.Equal(t, []int{0}, d.relevant)
	assert.NoError(t, d.addItem(6, "beta"))
	assert.Equal(t, []int{0}, d.relevant)
	assert.Equal(t, []int{0}, d.displayed)
}

func TestDropdownHandleLazyKeyEOF(t *testing.T) {
	d := newDropdown(t.Context())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyKey(tio, frame, 1, 1, keyEvent{}, false)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownHandleLazyKeyPasteIgnored(t *testing.T) {
	d := newDropdown(t.Context())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyKey(tio, frame, 1, 1, keyEvent{err: &pasteTextError{buf: []byte("a")}}, true)
	assert.NoError(t, err)
}

func TestDropdownHandleLazyKeyEnter(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	d.Items = []any{"seed"}
	bindDropdown(t, d)
	tio := newTestTermIO(20, 6)
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
	tio := newTestTermIO(20, 6)
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
	d := newDropdown(t.Context())
	tio := newTestTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	err := d.loadItem(tio, frame, itPair{err: io.EOF}, true, 1)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownLoadItemWriteError(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	bindDropdown(t, d)
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
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	bindDropdown(t, d)
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
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	bindDropdown(t, d)
	tio := &termIO{
		in:      bytes.NewBuffer([]byte{keyEnter}),
		out:     errWriterDropdown{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runRender(d.Ctx, tio, frame)
	assert.Error(t, err)
}

func TestDropdownHandleLazyKeyReadError(t *testing.T) {
	d := newDropdown(t.Context())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyKey(tio, frame, 1, 1, keyEvent{err: io.EOF}, true)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownRunRenderUsesMain(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	bindDropdown(t, d)
	tio := &termIO{
		in:      bytes.NewBuffer([]byte{keyEnter}),
		out:     &bytes.Buffer{},
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
	tio := &termIO{
		in:      bytes.NewBufferString("ab"),
		out:     &bytes.Buffer{},
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
	tio := &termIO{
		in:      bytes.NewBuffer(nil),
		out:     &bytes.Buffer{},
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
	tio := &termIO{
		in:      bytes.NewBuffer([]byte{keyEnter}),
		out:     &bytes.Buffer{},
		Width:   20,
		Height:  6,
		Restore: func() error { return nil },
	}
	frame := bytes.NewBuffer(nil)
	_, err := d.runRender(d.Ctx, tio, frame)
	assert.Error(t, err)
}

func TestDropdownNextLazyActionItem(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	d.Ctx = ctx
	tio := newTestTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	_, err := d.nextLazyAction(tio, frame, 1, 1, make(chan keyEvent))
	assert.Error(t, err)
}

func TestDropdownHandleLazyItemDoneWithItems(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	assert.NoError(t, d.parseTemplates())
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{}, false)
	assert.NoError(t, err)
	assert.True(t, d.iterDone)
}

func TestDropdownHandleLazyItemAddError(t *testing.T) {
	d := newDropdown(t.Context())
	d.activeItemTemplate = template.Must(template.New("active").Parse("{{ . }}"))
	d.inactiveItemTemplate = template.Must(template.New("inactive").Parse("{{call .}}"))
	d.trie = newTrie()
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{item: "x"}, true)
	assert.Error(t, err)
}

func TestDropdownAddItemDeduplicatesLazyRelevantIndexes(t *testing.T) {
	d := newDropdown(t.Context())
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
func notifyItemsLoaded(minItems int) (opt, <-chan struct{}) {
	ready := make(chan struct{})
	once := &sync.Once{}
	return opT(func(d *dropdown) error {
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
	itemsLoaded, ready := notifyItemsLoaded(3)
	opts = WithOptions(opts, itemsLoaded)
	out := captureOutput(cio)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy(cio.ctx, "Select", lazySeq("red", "green", "blue"), opts)
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
	itemsLoaded, ready := notifyItemsLoaded(2)
	opts := WithOptions(
		WithInput(in),
		WithOutput(io.Discard),
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
		value, err := DropdownLazy(ctx, "Select", lazySeq("red", "green"), opts)
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
	_, err := DropdownLazy(ctx, "Select", func(yield func(string, error) bool) {
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
	itemsLoaded, ready := notifyItemsLoaded(2)
	opts := WithOptions(
		WithInput(in),
		WithOutput(io.Discard),
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
	_, err = DropdownLazy(ctx, "First", func(yield func(string, error) bool) {
		yield("", io.EOF)
	}, opts)
	assert.Error(t, err)
	resCh := make(chan error, 1)
	go func() {
		_, err := DropdownLazy(ctx, "Second", lazySeq("a", "b"), opts)
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
	itemsLoaded, ready := notifyItemsLoaded(2)
	opts := WithOptions(
		WithInput(in),
		WithOutput(io.Discard),
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
	_, err = DropdownLazy(ctx, "First", func(yield func(string, error) bool) {
		yield("", io.EOF)
	}, opts)
	assert.Error(t, err)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy(ctx, "Second", lazySeq("a", "b"), opts)
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
	d := newDropdown(t.Context())
	err := WithDefault("alpha")(d)
	assert.NoError(t, err)
	assert.Equal(t, "alpha", d.oneMatch)
}

func TestWithOneMatch_singlePrefixMatchReturnsIndex(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 2, Name: "beta"},
	}
	err := WithDefault("beta")(d)
	assert.NoError(t, err)
	bindDropdown(t, d)
	_, err = d.renderInit(newTestTermIO(20, 6))
	var oneMatch oneHatch
	ok := errors.As(err, &oneMatch)
	assert.True(t, ok)
	// a unique prefix skips ranking, so the position is in item order
	assert.Equal(t, 1, int(oneMatch))
	assert.Equal(t, []int{0, 1}, d.relevant)
	assert.Equal(t, "", d.oneMatch)
}

func TestWithOneMatch_noMatchKeepsDropdown(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	d.Items = []any{
		dropdownHeuristicLabelItem{ID: 1, Name: "zeta"},
		dropdownHeuristicLabelItem{ID: 2, Name: "alpha"},
		dropdownHeuristicLabelItem{ID: 3, Name: "omega"},
	}
	err := WithDefault("alpa")(d)
	assert.NoError(t, err)
	bindDropdown(t, d)
	_, err = d.renderInit(newTestTermIO(20, 6))
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 0, 2}, d.relevant)
	assert.Equal(t, []int{1, 0, 2}, d.displayed)
	assert.Equal(t, "", d.oneMatch)
}

func TestWithOneMatch_multipleMatchesKeepDropdown(t *testing.T) {
	d := newDropdown(t.Context())
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
	d := newDropdown(t.Context())
	assert.Equal(t, 3, d.levenstein([]rune("kitten"), []rune("sitting")))
	assert.Equal(t, 0, d.levenstein([]rune("alpha"), []rune("alpha")))
	assert.Equal(t, 1, d.levenstein([]rune("alpa"), []rune("alpha")))
}

func TestDropdownLazyEmptySequence(t *testing.T) {
	cio, opts := testIOforDropdown(t, 20, 6)
	captureOutput(cio)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy(cio.ctx, "Empty", func(yield func(string, error) bool) {}, opts)
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
		value, err := DropdownLazy(cio.ctx, "Single", lazySeq("only"), opts)
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

func fixedTermOpt() opt {
	return opT(func(d *dropdown) error {
		d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return &termIO{in: in, out: out, Width: 20, Height: 6, Restore: func() error { return nil }}, nil
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

func TestDropdownLazyAppendAfterFilter(t *testing.T) {
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	for _, matches := range []struct {
		filter string
		want   []int
	}{
		{"alp", []int{0, 2}}, // one match grows to many
		{"alpi", []int{2}},   // zero matches before, one after
		{"a", []int{0, 1, 2}},
	} {
		d := newDropdown(t.Context())
		bindDropdown(t, d)
		for _, item := range []string{"alpha", "beta"} {
			_, _, err := d.handleLazyItem(tio, frame, 0, itPair{item: item}, true)
			assert.NoError(t, err)
		}
		assert.Equal(t, -1, d.applyInputEvent(tio, dropdownFilteredWith{Prefix: "alp"}, 2, 6))
		assert.Equal(t, []int{0}, d.relevant)
		if matches.filter != "alp" {
			assert.Equal(t, -1, d.applyInputEvent(tio, dropdownFilteredWith{Prefix: matches.filter}, 2, 6))
		}
		_, _, err := d.handleLazyItem(tio, frame, 0, itPair{item: "alpine"}, true)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(d.Items))
		assert.Equal(t, 3, len(d.active))
		if matches.filter == "alp" {
			assert.Equal(t, []int{0, 2}, d.relevant)
		}
	}
}

func TestDropdownLazyKeepsDefaultRanking(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithDefault("cat")(d))
	bindDropdown(t, d)
	tio := newTestTermIO(20, 6)
	frame := &bytes.Buffer{}
	// empty lazy dropdown renders before any item arrives
	_, err := d.renderInit(tio)
	assert.NoError(t, err)
	for _, item := range []string{"xxxx", "yyyy", "bat"} {
		_, _, err = d.handleLazyItem(tio, frame, 0, itPair{item: item}, true)
		assert.NoError(t, err)
	}
	assert.Equal(t, []int{2, 0, 1}, d.relevant)
	assert.Equal(t, []int{2, 0, 1}, d.displayed)
}

func TestDropdownLazyRankingKeepsSelectedItem(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithDefault("cat")(d))
	bindDropdown(t, d)
	tio := newTestTermIO(20, 10)
	frame := &bytes.Buffer{}
	for _, item := range []string{"xxxx", "yyyy"} {
		_, _, err := d.handleLazyItem(tio, frame, 0, itPair{item: item}, true)
		assert.NoError(t, err)
	}
	d.pressDown(len(d.displayed)) // user moved to "yyyy"
	_, _, err := d.handleLazyItem(tio, frame, 0, itPair{item: "bat"}, true)
	assert.NoError(t, err)
	assert.Equal(t, []int{2, 0, 1}, d.relevant)
	assert.Equal(t, 1, d.relevant[d.offset+d.selected])
}

func TestDropdownRenderRemembersLongestWidth(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"short", "a considerably longer item"}
	bindDropdown(t, d)
	tio := newTestTermIO(40, 6)
	first, err := d.renderInit(tio)
	assert.NoError(t, err)
	second, err := d.renderInit(tio)
	assert.NoError(t, err)
	assert.True(t, first > 20)
	assert.Equal(t, first, second)

	_, err = d.renderInit(tio)
	assert.NoError(t, err)
	tio.Width = 30
	var buf bytes.Buffer
	assert.NoError(t, d.render(tio, &buf))
	_ = d.addItem(6, "x") //nolint:errcheck // test cleanup
	assert.Equal(t, first, d.longest)
}

func TestDropdownLazyStopsProducerOnEarlyError(t *testing.T) {
	var started atomic.Int32
	seq := func(yield func(int, error) bool) {
		started.Add(1)
		for i := 0; yield(i, nil); i++ {
		}
	}
	_, err := DropdownLazy(t.Context(), "pick", seq,
		WithInput(bytes.NewBuffer(nil)), WithOutput(io.Discard),
		fixedTermOpt(), WithActiveItemTemplate("{{"))
	assert.Error(t, err)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), started.Load())
}

func TestDropdownLazyProducerStopsOnCancel(t *testing.T) {
	d := newDropdown(t.Context())
	d.IterBatchSize = 1
	done := make(chan struct{})
	seq := func(yield func(int, error) bool) {
		defer close(done)
		for i := 0; yield(i, nil); i++ {
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	d.startLazyProducer(ctx, func(yield func(any, error) bool) {
		seq(func(v int, err error) bool { return yield(v, err) })
	})
	time.Sleep(20 * time.Millisecond) // let the producer block on the full channel
	d.itItems = nil                   // the dropdown clears its field when done
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("producer outlived its context")
	}
}

func TestDropdownLazyStopsProducerAfterSelection(t *testing.T) {
	done := make(chan struct{})
	seq := func(yield func(int, error) bool) {
		defer close(done)
		for i := 0; yield(i, nil); i++ {
		}
	}
	in, w, err := os.Pipe()
	assert.NoError(t, err)
	t.Cleanup(func() { in.Close(); w.Close() }) //nolint:errcheck // test cleanup
	go func() {
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte{keyEnter}) //nolint:errcheck // test helper
	}()
	_, err = DropdownLazy(t.Context(), "pick", seq,
		WithInput(in), WithOutput(io.Discard), fixedTermOpt())
	assert.NoError(t, err)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("producer outlived DropdownLazy")
	}
}

func TestDropdownFilterEventsIgnoreRankingOrder(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, WithDefault("ap")(d))
	bindDropdown(t, d)
	tio := newTestTermIO(20, 6)
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

func TestDropdownLevensteinCountsRunes(t *testing.T) {
	d := newDropdown(t.Context())
	assert.Equal(t, 1, d.levenstein([]rune("é"), []rune("a")))
	assert.Equal(t, 1, d.levenstein([]rune("é"), []rune("è")))
	assert.Equal(t, 3, d.levenstein([]rune("sitting"), []rune("kitten")))
	assert.Equal(t, 3, d.levenstein([]rune(""), []rune("abc")))
	assert.Equal(t, 0, d.levenstein([]rune(""), []rune("")))
}

func TestDropdownLevensteinBoundedWork(t *testing.T) {
	d := newDropdown(t.Context())
	long := strings.Repeat("a", 100_000)
	assert.Equal(t, 0, d.levenstein([]rune(long), []rune(long)))
	assert.Equal(t, maxRankRunes, d.levenstein([]rune(long), []rune(strings.Repeat("b", 100_000))))
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
			opT(func(d *dropdown) error {
				d.makeTermIO = func(in io.Reader, w io.Writer) (*termIO, error) {
					return &termIO{
						in: in, out: w, Width: 20, Height: 6,
						Restore:  func() error { return nil },
						onResize: resize,
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
	_, err := writer.Write([]byte{keyEnter})
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
	io := quiet(t)
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
	io := quiet(t)
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
	io := quiet(t)
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
	io := quiet(t)
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
	_, err = Dropdown(ctx, "Pick", []string{"a"}, io, WithOneReturn(), WithActiveItemTemplate("{{"))
	assert.Error(t, err)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = Dropdown(cctx, "Pick", []string{"a"}, io, WithOneReturn())
	assert.ErrorIs(t, err, context.Canceled)
}

func TestConfirmDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault(true)))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault(false)))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("Yes")))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("y")))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault("no")))
	assert.True(t, !Confirm(ctx, "Go?", io))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault(42)))
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	assert.True(t, !Confirm(cctx, "Go?", io, WithDefault(true)))

	// the error behind the false is visible one level down
	_, err := Dropdown(ctx, "Go?", []string{"Yes", "No"}, io)
	assert.ErrorIs(t, err, ErrNoTTY)
}

func TestDropdownLazyDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()
	var started bool

	_, err := DropdownLazy(ctx, "Pick", seq(&started, nil, "a", "b"), io)
	assert.ErrorIs(t, err, ErrNoTTY)
	assert.True(t, !started)

	got, err := DropdownLazy(ctx, "Pick", seq(&started, nil, "alpha", "beta"), io, WithDefault("beta"))
	assert.NoError(t, err)
	assert.Equal(t, "beta", got)

	// a late second match is ambiguous
	_, err = DropdownLazy(ctx, "Pick", seq(&started, nil, "a", "b", "a"), io, WithDefault("a"))
	assert.ErrorIs(t, err, ErrInvalidOption)

	late := errors.New("late")
	_, err = DropdownLazy(ctx, "Pick", seq(&started, late, "a"), io, WithDefault("a"))
	assert.ErrorIs(t, err, late)

	_, err = DropdownLazy(ctx, "Pick", seq[string](&started, nil), io, WithDefault("a"))
	assert.ErrorIs(t, err, ErrEmptyLazyResult)

	got, err = DropdownLazy(ctx, "Pick", seq(&started, nil, "only"), io, WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, "only", got)
	// a singleton at first is not final
	_, err = DropdownLazy(ctx, "Pick", seq(&started, nil, "a", "b"), io, WithOneReturn())
	assert.ErrorIs(t, err, ErrNoTTY)

	typed, err := DropdownLazy(ctx, "Pick", seq(&started, nil, server{"a", 1}, server{"b", 2}), io,
		WithDefault(server{"b", 2}))
	assert.NoError(t, err)
	assert.Equal(t, server{"b", 2}, typed)

	cctx, cancel := context.WithCancel(ctx)
	_, err = DropdownLazy(cctx, "Pick", func(yield func(string, error) bool) {
		cancel()
		yield("a", nil)
	}, io, WithDefault("a"))
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

func TestDropdownLazySeedValidatedAfterExhaustion(t *testing.T) {
	for name, items := range map[string][]any{"missing": {1, 2}, "duplicate": {2, 2}} {
		t.Run(name, func(t *testing.T) {
			d := newDropdown(t.Context())
			d.target = choiceTarget{}
			d.def = defaultValue{set: true, raw: 3}
			if name == "duplicate" {
				d.def.raw = 2
			}
			d.seedLazy = true
			d.Items = items
			err := d.validateLazySeed()
			assert.ErrorIs(t, err, ErrInvalidOption)
		})
	}
}

func TestDropdownLazyAutomaticAnswerCancelsWhileProducerBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})
	defer close(release)
	seq := func(_ func(string, error) bool) {
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, err := DropdownLazy(ctx, "Pick", seq, WithDefault("a"), WithOutput(&bytes.Buffer{}))
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("prompt stayed blocked after cancellation")
	}
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
func scripted(t *testing.T, th Theme, width int, keys ...string) *scriptedTerminal {
	t.Helper()
	useTheme(t, th)
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
	s.in = &chunkReader{chunks: chunks}
	return s
}

// makeTermIO exposes scripted terminal dimensions and records restoration.
func (s *scriptedTerminal) makeTermIO(in io.Reader, out io.Writer) (*termIO, error) {
	restore := func() error { *s.restored = true; return nil }
	return &termIO{in: in, out: out, Width: s.width, Height: 10, Restore: restore}, nil
}

// themedDropdownOpt routes a themed dropdown through a scripted terminal.
func themedDropdownOpt(t *testing.T, th Theme, width int, keys ...string) (o opt, out *bytes.Buffer, restored *bool) {
	t.Helper()
	s := scripted(t, th, width, keys...)
	return opT(func(d *dropdown) error {
		d.in, d.out, d.makeTermIO = s.in, s.out, s.makeTermIO
		return nil
	}), s.out, s.restored
}

// boundDropdown is a themed dropdown over items, parsed and bound to a
// terminal of the given size, which render draws a frame on.
func boundDropdown(t *testing.T, th Theme, width, height int, items []any, o ...opt) (*dropdown, *termIO) {
	t.Helper()
	useTheme(t, th)
	d := newDropdown(t.Context())
	d.Label, d.Items = "Fruit", items
	assert.NoError(t, opts(o).Apply(d))
	assert.NoError(t, d.parseTemplates())
	tt := &termIO{out: &bytes.Buffer{}, Width: width, Height: height}
	assert.NoError(t, d.bind(defaultIO.bindTheme(t.Context(), d.theme, d.out, tt)))
	assert.NoError(t, d.renderChrome())
	return d, tt
}

// frame draws one frame of d on tt.
func frame(t *testing.T, d *dropdown, tt *termIO) string {
	t.Helper()
	var buf bytes.Buffer
	assert.NoError(t, d.render(tt, &buf))
	return buf.String()
}

var fruits = []string{"apple", "banana", "cherry"}

func TestThemedDropdownGuidedFrameAndAnswer(t *testing.T) {
	o, out, restored := themedDropdownOpt(t, asciiTheme(false), 30, "\x1b[B", "\r")
	got, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, "banana", got)
	assert.True(t, *restored)
	erase := "\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"
	assert.Equal(t, ""+
		// the label has its own row, the rows and the hint are behind the guide
		"\r? Fruit\n\r| > apple\n\r|   banana\n\r|   cherry\n\r| up/down move · type filter\n\r"+
		// the next frame erases all five rows first
		erase+
		"\r? Fruit\n\r|   apple\n\r| > banana\n\r|   cherry\n\r| up/down move · type filter\n\r"+
		// confirming erases the frame once, leaving the rows above it alone
		erase+
		"+ Fruit · banana\n", out.String())
}

func TestDropdownConfirmClearsFrameOnce(t *testing.T) {
	var out bytes.Buffer
	_, err := Dropdown(t.Context(), "Fruit", fruits, WithOutput(&out),
		WithInput(&chunkReader{chunks: [][]byte{[]byte("\r")}}),
		WithLabelTemplate("{{ . }}"), WithActiveItemTemplate("+ {{ . }}"),
		WithInactiveItemTemplate("- {{ . }}"), WithAnswerTemplate("{{ .Answer }}"),
		opT(func(d *dropdown) error {
			d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{in: in, out: out, Width: 40, Height: 10, Restore: func() error { return nil }}, nil
			}
			return nil
		}))
	assert.NoError(t, err)
	assert.Equal(t, ""+
		"\rFruit \n\r│ + apple\n\r│ - banana\n\r│ - cherry\n\r│ up/down move · type filter\n\r"+
		"\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"+
		"apple\n", out.String())
}

func TestThemedDropdownInlineIndentsRowsUnderLabel(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, asciiTheme(true), 30, "\x1b[B", "\r")
	_, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(out.String(), ""+
		"\r? Fruit > apple\n\r          banana\n\r          cherry\n\rup/down move · type filter\n\r"))
	assert.Contains(t, out.String(), "\r? Fruit   apple\n\r        > banana\n")
}

func TestThemedDropdownInlineLabelTooWideTakesItsRow(t *testing.T) {
	d, tt := boundDropdown(t, asciiTheme(true), 12, 10, []any{"apple", "banana"})
	assert.Equal(t, "\r? Fruit\n\r> apple\n\r  banana\n\rup/down mo…\n\r", frame(t, d, tt))
}

func TestThemedDropdownMoreRowAndShortTerminals(t *testing.T) {
	items := []any{"a", "b", "c", "d", "e"}
	for _, tc := range []struct {
		height int
		want   string
	}{
		{10, "\r? Fruit\n\r| > a\n\r|   b\n\r|   c\n\r|   d\n\r|   e\n\r| up/down move · type filter\n\r"},
		// three of five rows fit with the count of the rest; the cursor row
		// below the frame leaves no row for the hint, which goes first
		{6, "\r? Fruit\n\r| > a\n\r|   b\n\r|   c\n\r| . 2 more\n\r"},
		// then the count, then the label
		{4, "\r? Fruit\n\r| > a\n\r|   b\n\r"},
		{3, "\r? Fruit\n\r| > a\n\r"},
	} {
		t.Run(strconv.Itoa(tc.height), func(t *testing.T) {
			d, tt := boundDropdown(t, asciiTheme(false), 40, tc.height, items)
			assert.Equal(t, tc.want, frame(t, d, tt))
			assert.Equal(t, strings.Count(tc.want, "\n"), d.height())
		})
	}
}

func TestThemedDropdownMoreCountsRowsBelowAfterScrolling(t *testing.T) {
	d, tt := boundDropdown(t, asciiTheme(false), 40, 6, []any{"a", "b", "c", "d", "e"})
	frame(t, d, tt)
	for range 4 {
		d.pressDown(len(d.displayed))
	}
	// at the bottom nothing is below, and the count row is gone
	assert.Equal(t, "\r? Fruit\n\r|   c\n\r|   d\n\r| > e\n\r| up/down move · type filter\n\r", frame(t, d, tt))
}

type themedFruit struct{ Name, Color string }

func TestThemedDropdownProjectionMatchesAndAnswers(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}, {"cherry", "blue"}}
	// "b" is the start of a detail too, but only the projected name matches
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 40, "b", "\r")
	got, err := Dropdown(t.Context(), "Fruit", items, o, WithTemplate(".Name", ".Color"))
	assert.NoError(t, err)
	assert.Equal(t, items[1], got)
	s := out.String()
	// details are on the active row only
	assert.Contains(t, s, "\r| > apple red\n\r|   banana\n\r|   cherry\n")
	assert.Contains(t, s, "\r? Fruit\n\r| > banana yellow\n\r| up/down move · type filter\n")
	assert.True(t, strings.HasSuffix(s, "+ Fruit · banana\n"))
}

func TestThemedDropdownMultiFieldProjects(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}}
	d, tt := boundDropdown(t, asciiTheme(false), 40, 10, []any{items[0], items[1]}, WithTemplate(`.Name ", " .Color`))
	assert.Equal(t,
		"\r? Fruit\n\r| > apple, red\n\r|   banana, yellow\n\r| up/down move · type filter\n\r",
		frame(t, d, tt))
}

func TestThemedDropdownExplicitTemplatesWinInEitherOrder(t *testing.T) {
	active := WithActiveItemTemplate(`* {{ . }}`)
	project := WithTemplate(`(printf "<%s>" .)`)
	for name, o := range map[string][]opt{
		"explicit first":   {active, project},
		"projection first": {project, active},
	} {
		t.Run(name, func(t *testing.T) {
			d, tt := boundDropdown(t, asciiTheme(false), 40, 10, []any{"apple", "banana"}, o...)
			// the explicit row brings its own marker; the other row is projected
			assert.Equal(t,
				"\r? Fruit\n\r| * apple\n\r|   <banana>\n\r| up/down move · type filter\n\r",
				frame(t, d, tt))
		})
	}
}

func TestThemedDropdownExplicitAnswerKeepsRawPayload(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}}
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 40, "\r")
	_, err := Dropdown(t.Context(), "Fruit", items, o,
		WithAnswerTemplate(`{{ .Label }}={{ .Answer.Name }}/{{ .Answer.Color }}`), WithTemplate(".Name"))
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), "\rFruit=apple/red\n"))
}

func TestThemedDropdownHiddenAnswerRetainsNothing(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Dropdown.Answer = Hidden
	o, out, _ := themedDropdownOpt(t, th, 40, "\r")
	_, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[4A\r"))

	o, out, _ = themedDropdownOpt(t, asciiTheme(false), 40, "\r")
	_, err = Dropdown(t.Context(), "Fruit", fruits, o, WithHide())
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[4A\r"))
}

func TestThemedDropdownSlotPrecedence(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Choice.Inactive = `{{ printf "-%s" .Value }}`
	th.Templates.Dropdown.Active = `[{{ .Value }}]`
	th.Templates.Choice.More = Hidden
	th.Templates.Prompt.Hint = Hidden
	th.Templates.Dropdown.Label = `{{ .Label }}:`
	d, tt := boundDropdown(t, th, 40, 4, []any{"a", "b", "c", "d"})
	// shared and family edits apply; hidden slots reserve no row
	assert.Equal(t, "\rFruit:\n\r| > [a]\n\r|   -b\n\r", frame(t, d, tt))
}

func TestThemedDropdownPerCallMoreAndLabelKeepPayloads(t *testing.T) {
	d, tt := boundDropdown(t, asciiTheme(false), 40, 5, []any{"a", "b", "c", "d"},
		WithMoreItemsTemplate(`{{ .More }}/{{ .Total }}`), WithLabelTemplate(`<{{ . }}>`))
	assert.Equal(t, "\r<Fruit> \n\r| > a\n\r|   b\n\r| 2/4\n\r", frame(t, d, tt))
}

func TestThemedDropdownKVShowsKeys(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 40, "\x1b[B", "\r")
	k, v, err := DropdownKV(t.Context(), "Pick", map[string]int{"b": 2, "a": 1}, o)
	assert.NoError(t, err)
	assert.Equal(t, "b", k)
	assert.Equal(t, 2, v)
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > a\n\r|   b\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · b\n"))
}

func TestThemedDropdownIndexKeepsSourceIndexOfDuplicates(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 40, "\x1b[B", "\r")
	i, err := DropdownIndex(t.Context(), "Pick", []any{"same", "same"}, o)
	assert.NoError(t, err)
	assert.Equal(t, 1, i)
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · same\n"))
}

func TestThemedDropdownTypedDefaultSeedsFirstRow(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 40, "\r")
	got, err := Dropdown(t.Context(), "Pick", []int{1, 2, 3}, o, WithDefault(2))
	assert.NoError(t, err)
	assert.Equal(t, 2, got)
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > 2\n\r|   1\n\r|   3\n"))
}

func TestThemedDropdownOneReturnAnswersWithoutFrame(t *testing.T) {
	o, out, restored := themedDropdownOpt(t, asciiTheme(false), 40)
	got, err := Dropdown(t.Context(), "Fruit", []string{"apple"}, o, WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, "apple", got)
	assert.True(t, !*restored) // no terminal was acquired
	assert.Equal(t, "+ Fruit · apple\n", out.String())
}

func TestThemedDropdownAutomaticAnswerMatchesProjection(t *testing.T) {
	strictOutput(t)
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	// no presentation slot runs without a terminal: only the projection
	th.Templates.Prompt.Label = `{{ fails .Label }}`
	th.Templates.Choice.Active = `{{ fails .Value }}`
	th.Templates.Choice.Value = `{{ printf "z%s" . }}`
	useTheme(t, th)
	var out bytes.Buffer
	got, err := Dropdown(t.Context(), "Fruit", fruits, WithOutput(&out), WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, "banana", got)

	got, err = Dropdown(t.Context(), "Fruit", fruits, WithOutput(&out),
		WithTemplate(`(printf "y%s" .)`), WithDefault("yc"))
	assert.NoError(t, err)
	assert.Equal(t, "cherry", got)

	// a per-call inactive template without a projection is matched as before
	got, err = Dropdown(t.Context(), "Fruit", fruits, WithOutput(&out),
		WithInactiveItemTemplate(`x{{ . }}`), WithDefault("xa"))
	assert.NoError(t, err)
	assert.Equal(t, "apple", got)
	assert.Equal(t, "", out.String())
}

func TestThemedDropdownSearchSkipsDecoration(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Choice.Active = `{{ .Value }} zzz`
	th.Templates.Choice.Inactive = `{{ .Value }} zzz`
	d, tt := boundDropdown(t, th, 40, 10, []any{"apple", "banana"})
	frame(t, d, tt)
	// the presentation of the rows is not searchable, only their content
	assert.Equal(t, 0, len(d.trie.Prefix("zzz")))
	assert.Equal(t, []int{1}, d.trie.Prefix("b"))
	assert.Equal(t, []string{"apple", "banana"}, d.search)

	// a late item is projected and matched like the others
	assert.NoError(t, d.addItem(tt.Height, "blueberry"))
	assert.Equal(t, []int{1, 2}, d.trie.Prefix("b"))
	assert.Equal(t, "blueberry", d.content[2].String())
}

func TestThemedDropdownLazyOneReturnAndProducerError(t *testing.T) {
	o, out, restored := themedDropdownOpt(t, asciiTheme(false), 40)
	got, err := DropdownLazy(t.Context(), "Fruit", lazySeq("apple"), o, WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, "apple", got)
	assert.True(t, *restored)
	assert.True(t, strings.HasSuffix(out.String(), "+ Fruit · apple\n"))

	boom := errors.New("boom")
	failing := func(yield func(string, error) bool) {
		if yield("apple", nil) {
			yield("", boom)
		}
	}
	o, out, restored = themedDropdownOpt(t, asciiTheme(false), 40)
	_, err = DropdownLazy(t.Context(), "Fruit", failing, o)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedDropdownHelperErrorRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Templates.Choice.Inactive = `{{ fails .Value }}`
	o, _, restored := themedDropdownOpt(t, th, 40, "\r")
	_, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Templates.Choice.Inactive")
	assert.True(t, *restored)
}

func TestThemedConfirmGuidedFrameAndYes(t *testing.T) {
	o, out, restored := themedDropdownOpt(t, asciiTheme(false), 40, "\r")
	assert.True(t, Confirm(t.Context(), "Sure?", o))
	assert.True(t, *restored)
	assert.Equal(t, ""+
		"\r? Sure?\n\r| > Yes\n\r|   No\n\r| up/down move · type filter\n\r"+
		"\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"+
		"+ Sure? · Yes\n", out.String())
}

func TestThemedConfirmNoIsNormalAnswer(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 40, "\x1b[B", "\r")
	assert.True(t, !Confirm(t.Context(), "Sure?", o))
	// No is retained with the completion mark, not as an error
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[3A\r+ Sure? · No\n"))
	assert.True(t, !strings.Contains(out.String(), "!"))

	o, out, _ = themedDropdownOpt(t, asciiTheme(true), 40, "n", "\r")
	assert.True(t, !Confirm(t.Context(), "Sure?", o))
	assert.True(t, strings.HasPrefix(out.String(), "\r? Sure? > Yes\n\r          No\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Sure? · No\n"))
}

func TestThemedConfirmfFormatsLabel(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 40, "\r")
	prev := confirmRunner
	t.Cleanup(func() { confirmRunner = prev })
	confirmRunner = func(ctx context.Context, action string, _ ...opt) bool {
		return defaultConfirmRunner(ctx, action, o)
	}
	assert.True(t, Confirmf(t.Context(), "Deploy %s?", "api"))
	assert.True(t, strings.HasPrefix(out.String(), "\r? Deploy api?\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Deploy api? · Yes\n"))
}

func TestThemedConfirmSlotPrecedence(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Prompt.Answer = `{{ .Label }} {{ .Value }}/{{ .Count }}`
	th.Templates.Choice.Inactive = `-{{ .Value }}`
	th.Templates.Dropdown.Active = `dropdown {{ .Value }}`
	th.Templates.Dropdown.Label = `dropdown {{ .Label }}`
	th.Templates.Confirm.Label = `{{ .Label }} [y/n]`
	th.Templates.Confirm.Hint = Hidden
	o, out, _ := themedDropdownOpt(t, th, 40, "\x1b[B", "\r")
	assert.True(t, !Confirm(t.Context(), "Sure?", o))
	// shared and Confirm edits apply, Dropdown edits stay with the dropdown
	assert.True(t, strings.HasPrefix(out.String(), "\rSure? [y/n]\n\r| > Yes\n\r|   -No\n\r"+
		"\x1b[3A"))
	assert.True(t, strings.HasSuffix(out.String(), "Sure? No/1\n"))
}

func TestThemedConfirmHiddenAnswerAndPerCallOptions(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Confirm.Answer = Hidden
	o, out, _ := themedDropdownOpt(t, th, 40, "\r")
	assert.True(t, Confirm(t.Context(), "Sure?", o))
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[3A\r"))

	// per-call templates keep their raw payloads over the theme
	o, out, _ = themedDropdownOpt(t, asciiTheme(false), 40, "\x1b[B", "\r")
	assert.True(t, !Confirm(t.Context(), "Sure?", o,
		WithActiveItemTemplate(`* {{ . }}`), WithAnswerTemplate(`{{ .Label }}={{ .Answer }}`)))
	assert.True(t, strings.HasPrefix(out.String(), "\r? Sure?\n\r| * Yes\n\r|   No\n"))
	assert.True(t, strings.HasSuffix(out.String(), "Sure?=No\n"))
}

func TestThemedConfirmDefaultsWithoutTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	// without a terminal only the projection runs, never a presentation slot
	th.Templates.Prompt.Label = `{{ fails .Label }}`
	th.Templates.Confirm.Active = `{{ fails .Value }}`
	th.Templates.Confirm.Inactive = `{{ fails .Value }}`
	th.Templates.Confirm.Answer = `{{ fails .Value }}`
	useTheme(t, th)
	io := quiet(t)
	ctx := t.Context()
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault(true)))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault(false)))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("Yes")))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("y")))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault("no")))
	assert.True(t, !Confirm(ctx, "Go?", io))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault(42)))

	// a Confirm projection is what hints match; an exact boolean still wins
	th.Templates.Confirm.Value = `{{ if eq . "Yes" }}sure{{ else }}never{{ end }}`
	useTheme(t, th)
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault("s")))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault("never")))
	assert.True(t, Confirm(ctx, "Go?", io, WithDefault(true)))
	assert.True(t, !Confirm(ctx, "Go?", io, WithDefault("y")))
}

func TestThemedConfirmHelperErrorIsFalseAndRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Templates.Confirm.Inactive = `{{ fails .Value }}`
	o, out, restored := themedDropdownOpt(t, th, 40, "\r")
	assert.True(t, !Confirm(t.Context(), "Sure?", o))
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Sure?"))

	// the same theme leaves a dropdown alone
	o, _, _ = themedDropdownOpt(t, th, 40, "\r")
	_, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
}

func TestThemedConfirmRolesMarkYesAndNoAsCompleted(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	th := asciiTheme(false)
	th.Light = true
	for _, keys := range [][]string{{"\r"}, {"\x1b[B", "\r"}} {
		o, out, _ := themedDropdownOpt(t, th, 40, keys...)
		Confirm(t.Context(), "Sure?", o)
		roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), out, nil).roles
		assert.True(t, len(roles.success) > 0 && len(roles.failure) > 0)
		answer := out.String()[strings.LastIndex(out.String(), "\x1b[3A\r")+len("\x1b[3A\r"):]
		assert.True(t, strings.HasPrefix(answer, string(roles.success)+"+"))
		assert.True(t, !strings.Contains(answer, string(roles.failure)))
	}
}

func TestThemedDropdownRolesStyleFocusAndRows(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	th := asciiTheme(false)
	th.Light = true
	d, tt := boundDropdown(t, th, 40, 10, []any{"a", "b"})
	got := frame(t, d, tt)
	// the focus mark and the active value take the accent, inactive values the foreground
	roles := d.look.roles
	assert.True(t, len(roles.accent) > 0 && len(roles.foreground) > 0)
	assert.Contains(t, got, string(roles.accent)+">")
	assert.Contains(t, got, string(roles.accent)+"a")
	assert.Contains(t, got, string(roles.foreground)+"b")
}

func TestThemedDropdownFullHeightFrameKeepsLabelOnScreen(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, asciiTheme(false), 60, "\x1b[B", "\x1b[B", "\x1b[A", "\x1b[B", "\r")
	six := opT(func(d *dropdown) error {
		next := d.makeTermIO
		d.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			tt, err := next(in, out)
			tt.Height = 6
			return tt, err
		}
		return nil
	})
	got, err := Dropdown(t.Context(), "Fruit", []string{"a", "b", "c", "d", "e"}, o, six)
	assert.NoError(t, err)
	assert.Equal(t, "c", got)
	frameStaysOnScreen(t, out.String())
}
