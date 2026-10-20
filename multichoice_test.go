// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/assert"
)

func TestMultichoiceRenderInitializes(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"one", "two"}
	io := newTestTermIO(20, 6)

	frame := &bytes.Buffer{}
	assert.NoError(t, m.prepare(nil))

	assert.NoError(t, m.render(io, frame))
}

func TestMultichoiceRenderMoreItems(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"short", "veryverylong", "mid", "tail"}
	m.active = 0
	io := newTestTermIO(5, 4)
	frame := &bytes.Buffer{}
	assert.NoError(t, m.prepare(nil))

	assert.NoError(t, m.render(io, frame))
}

func TestMultichoiceRunNoSpace(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"one"}
	m.out = io.Discard
	m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   10,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	err := m.run(m.Ctx)
	assert.ErrorIs(t, err, ErrNoSpace)
}

func TestMultichoiceRunHandlesKeys(t *testing.T) {
	reader := &chunkReader{chunks: [][]byte{
		{0x1b, 0x5b, 0x42},
		{' '},
		{'z'},
		{'o'},
		{0x7f},
		{0x1b, 0x5b, 0x41},
		{keyEnter},
	}}
	m := newMultichoice(t.Context())
	m.Items = []any{"one", "two", "three"}
	m.in = reader
	m.out = &bytes.Buffer{}
	m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   40,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	assert.NoError(t, m.run(m.Ctx))
}

func TestMultichoiceRunIgnoresSpecialKeys(t *testing.T) {
	reader := &chunkReader{chunks: [][]byte{
		[]byte("\x1b[3~"),
		[]byte("\x1bx"),
		{0x1b, 0x1b, 0x5b, 0x42}, // Esc, then down
		{' '},
		{keyEnter},
	}}
	m := newMultichoice(t.Context())
	m.Items = []any{"one", "two", "three"}
	m.in = reader
	m.out = &bytes.Buffer{}
	m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   40,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	assert.NoError(t, m.run(m.Ctx))
	assert.Equal(t, []bool{false, true, false}, m.selected)
}

func TestMultichoiceRunContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m := newMultichoice(ctx)
	m.Items = []any{"one"}
	m.in = &chunkReader{chunks: [][]byte{{byte(keyEnter)}}}
	m.out = &bytes.Buffer{}
	m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   40,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	err := m.run(m.Ctx)
	assert.Error(t, err)
}

func TestMultichoiceRunCoalescedKeys(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"one"}
	m.in = &chunkReader{chunks: [][]byte{{'a', 'b'}, {byte(keyEnter)}}}
	m.out = &bytes.Buffer{}
	m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   40,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	assert.NoError(t, m.run(m.Ctx))
}

func TestMultichoiceRunReadError(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"one"}
	m.in = bytes.NewBuffer(nil)
	m.out = &bytes.Buffer{}
	m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   40,
			Height:  6,
			Restore: func() error { return nil },
		}, nil
	}
	err := m.run(m.Ctx)
	assert.Error(t, err)
}

func TestMultichoiceClampRowsKeepsActiveItemVisible(t *testing.T) {
	m := &multichoice{}
	m.relevant = []int{0, 1, 2, 3, 4, 5, 6}
	m.displayed = m.relevant[:5]
	m.active = 4

	m.clampRows(2) // two rows

	if got := m.relevant[m.offset+m.active]; got != 4 {
		t.Fatalf("active item changed: got %d, want 4", got)
	}
	if m.active < 0 || m.active >= len(m.displayed) {
		t.Fatalf("active %d outside displayed %d", m.active, len(m.displayed))
	}
}

func TestMultichoiceRunRerendersOnResize(t *testing.T) {
	keys, typing := io.Pipe()
	t.Cleanup(func() { _ = typing.Close() }) //nolint:errcheck // test cleanup
	resized := make(chan struct{})
	out := &syncBuffer{}
	var tio *termIO
	m := newMultichoice(t.Context())
	m.Items = []any{"one", "two", "three", "four", "five", "six"}
	m.in = keys
	m.out = out
	m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		tio = &termIO{
			in:       in,
			out:      out,
			Width:    40,
			Height:   8,
			Restore:  func() error { return nil },
			onResize: resized,
		}
		return tio, nil
	}
	done := make(chan error, 1)
	go func() { done <- m.run(m.Ctx) }()
	waitBufferContains(t, out, "2 more")

	// shrink the terminal without pressing a key
	tio.Height = 4
	select {
	case resized <- struct{}{}:
	case <-time.After(time.Second):
		t.Fatal("resize not delivered")
	}
	waitBufferContains(t, out, "4 more")

	_, err := typing.Write([]byte{keyEnter})
	assert.NoError(t, err)
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("multichoice did not return after Enter")
	}
}

func waitBufferContains(t *testing.T, out *syncBuffer, part string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(out.String(), part) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for output containing %q, got %q", part, out.String())
		}
		time.Sleep(time.Millisecond)
	}
}

// newPipeMultichoice isolates reader handoff tests from real terminal setup.
func newPipeMultichoice(ctx context.Context, in io.Reader) *multichoice {
	m := newMultichoice(ctx)
	m.Items = []any{"one", "two", "three"}
	m.in = in
	m.out = &bytes.Buffer{}
	m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{in: in, out: out, Width: 40, Height: 8, Restore: func() error { return nil }}, nil
	}
	return m
}

func TestMultichoiceRunReadsPendingEnter(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	m := newPipeMultichoice(t.Context(), pr)
	_, err = pw.WriteString("\x1b[B\r")
	assert.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- m.run(m.Ctx) }()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Enter left in pending was not read")
	}
	assert.Equal(t, 1, m.active)
}

func TestMultichoiceRunCancelledWrappedReadKeepsNextKey(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	in := &notifyingReader{File: pr, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := newPipeMultichoice(ctx, in)
	finished := make(chan error, 1)
	go func() { finished <- m.run(m.Ctx) }()
	select {
	case <-in.started:
	case <-time.After(time.Second):
		t.Fatal("multichoice did not start reading")
	}
	cancel()
	select {
	case err := <-finished:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled multichoice did not return")
	}
	// the blocked read completes only now, after its prompt is gone
	_, err = pw.Write([]byte{'x', keyEnter})
	assert.NoError(t, err)
	next := newInput(t.Context(), "next")
	next.ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	next.in, next.out = in, &bytes.Buffer{}
	next.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			input:   sharedInputState(in),
			Width:   20,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	assert.NoError(t, next.parseTemplates())
	got, err := next.run(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
}

// preparedMultichoice builds item state for tests that bypass terminal interaction.
func preparedMultichoice(t *testing.T, items []any, o ...opt) *multichoice {
	t.Helper()
	m := newMultichoice(t.Context())
	m.Items = items
	if err := m.prepare(o); err != nil {
		t.Fatal(err)
	}
	if err := m.resolveDefault(); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMultichoiceStateAndGeometry(t *testing.T) {
	m := preparedMultichoice(t, []any{"one", "two", "three", "four", "five", "six"})
	m.clampRows(3)
	for _, want := range []struct {
		key            rune
		offset, active int
	}{{'↓', 0, 1}, {'↓', 0, 2}, {'↓', 1, 2}, {'↑', 1, 1}, {'↑', 1, 0}, {'↑', 0, 0}} {
		m.handleKey(want.key)
		m.clampRows(3)
		if m.offset != want.offset || m.active != want.active {
			t.Fatalf("%c: offset %d active %d", want.key, m.offset, m.active)
		}
	}
	m.handleKey(' ')
	m.filter("zz")
	m.handleKey(' ')
	if m.focused() != -1 || m.count != 1 || !m.handleKey(keyEnter) {
		t.Fatal("empty filter must preserve hidden checked items")
	}
	m.filter("")
	m.handleKey('↓')
	m.clampRows(3)
	source := m.focused()
	m.clampRows(1)
	m.clampRows(10)
	if m.focused() != source || m.count != 1 {
		t.Fatal("resize changed identity")
	}
	for _, query := range []string{"e\u0301", "👨‍👩‍👧", "🇺🇦", "👍🏽", "\u6f22"} {
		m.filter(query)
		m.handleKey(0x7f)
		if m.typed != "" {
			t.Fatalf("backspace left %q", m.typed)
		}
	}
	for _, width := range []int{1, 2, 6, 20} {
		m := preparedMultichoice(
			t,
			[]any{"\u6f22\u5b57", "👨‍👩‍👧 family", "e\u0301", "\x1b[31mred\x1b[0m", "first\nsecond"},
		)
		m.Label = "very long label"
		m.labelBuf = text(m.Label)
		tio := newTestTermIO(width, 6)
		var frame bytes.Buffer
		if err := m.render(tio, &frame); err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(frame.String(), "\n") {
			if !utf8.ValidString(line) || text(line).width() > width {
				t.Fatalf("width %d: %q", width, line)
			}
		}
	}
	m = preparedMultichoice(t, []any{"one"})
	var frame bytes.Buffer
	if err := m.render(newTestTermIO(0, 4), &frame); !errors.Is(err, ErrNoSpace) {
		t.Fatal(err)
	}
	if err := m.render(newTestTermIO(20, 2), &frame); !errors.Is(err, ErrNoSpace) {
		t.Fatal(err)
	}
}

func TestMultichoicePreparationAndCapabilities(t *testing.T) {
	m := preparedMultichoice(t, []any{"one", "two"}, WithDefault("one"), WithDefault("two"))
	if m.typed != "" || !reflect.DeepEqual(m.indices(), []int{1}) {
		t.Fatal(m.typed, m.indices())
	}
	m.handleKey(' ')
	m.filter("one")
	m.filter("")
	if m.count != 0 {
		t.Fatal("default was reapplied")
	}
	for _, option := range []opt{WithTemplate(".Name"), WithFieldTemplate("Name"), WithActiveItemTemplate("{{.}}"), WithInactiveItemTemplate("{{.}}"), WithMoreItemsTemplate("{{.More}}")} {
		if err := option(newMultichoice(t.Context())); err != nil {
			t.Fatal(err)
		}
		if err := option(newDropdown(t.Context())); err != nil {
			t.Fatal(err)
		}
		if err := option(newInput(t.Context(), "Input")); !errors.Is(err, ErrWrongWidget) {
			t.Fatalf("input accepted choice option: %v", err)
		}
	}
	var out bytes.Buffer
	invalid := newMultichoice(t.Context())
	invalid.Items = []any{"one"}
	invalid.in = strings.NewReader("\r")
	invalid.out = &out
	acquired := false
	invalid.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) { acquired = true; return nil, ErrNoTTY }
	for _, option := range []opt{WithAnswerTemplate("{{"), WithInactiveItemTemplate("{{.Missing}}"), WithLabelTemplate("{{.Missing}}")} {
		if err := invalid.prepare([]opt{option}); err == nil {
			t.Fatal("invalid preparation passed")
		}
		if acquired || out.Len() != 0 {
			t.Fatal("preparation acquired terminal")
		}
		invalid = newMultichoice(t.Context())
		invalid.Items = []any{"one"}
		invalid.out = &out
	}
}

type choiceFailWriter struct {
	err        error
	short      bool
	answerOnly bool
}

// Write injects output failures at frame or answer time for lifecycle tests.
func (w choiceFailWriter) Write(p []byte) (int, error) {
	if w.answerOnly && !bytes.HasPrefix(p, []byte("ANSWER")) {
		return len(p), nil
	}
	if w.short {
		return len(p) / 2, nil
	}
	return 0, w.err
}

func TestMultichoiceLifecycleFailures(t *testing.T) {
	sentinel := errors.New("writer broke")
	restoreErr := errors.New("restore broke")
	for _, tc := range []struct {
		name    string
		writer  io.Writer
		keys    string
		height  int
		restore error
		want    error
	}{
		{"success", io.Discard, " \r", 8, nil, nil},
		{"EOF", io.Discard, "", 8, nil, io.EOF},
		{"writer", choiceFailWriter{err: sentinel}, "\r", 8, nil, sentinel},
		{"short write", choiceFailWriter{short: true}, "\r", 8, nil, io.ErrShortWrite},
		{"answer", choiceFailWriter{err: sentinel, answerOnly: true}, "\r", 8, nil, sentinel},
		{"restore", io.Discard, "\r", 8, restoreErr, restoreErr},
		{"no space", io.Discard, "\r", 2, nil, ErrNoSpace},
		{"primary and cleanup", choiceFailWriter{err: sentinel}, "\r", 8, restoreErr, sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMultichoice(t.Context())
			m.Items = []any{"one"}
			m.in = strings.NewReader(tc.keys)
			m.out = tc.writer
			m.AnswerTemplate = "ANSWER {{.Count}}"
			restores := 0
			m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				return &termIO{
					in:      in,
					out:     out,
					Width:   40,
					Height:  tc.height,
					Restore: func() error { restores++; return tc.restore },
				}, nil
			}
			err := m.run(m.Ctx)
			if !errors.Is(err, tc.want) || restores != 1 {
				t.Fatalf("%v, restores %d", err, restores)
			}
			if tc.restore != nil && !errors.Is(err, tc.restore) {
				t.Fatal(err)
			}
		})
	}
	for range 20 {
		m := newMultichoice(t.Context())
		m.Items = []any{"one"}
		if err := m.run(m.Ctx, WithInput(strings.NewReader("\r")), WithOutput(io.Discard)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMultichoiceBracketedPasteSplitsAndHandoff(t *testing.T) {
	input := "\x1b[200~two\r\n\t\x03\x04\x1b[201~ \r \r"
	for split := 1; split < len(input); split++ {
		t.Run(strconv.Itoa(split), func(t *testing.T) {
			in := io.MultiReader(strings.NewReader(input[:split]), strings.NewReader(input[split:]))
			first, err := Multichoice(
				t.Context(),
				"First",
				[]string{"one", "two"},
				WithInput(in),
				WithOutput(io.Discard),
			)
			if err != nil || !reflect.DeepEqual(first, []string{"two"}) {
				t.Fatalf("%v, %v", first, err)
			}
			next, err := Multichoice(t.Context(), "Next", []string{"next"}, WithInput(in), WithOutput(io.Discard))
			if err != nil || !reflect.DeepEqual(next, []string{"next"}) {
				t.Fatalf("handoff: %v, %v", next, err)
			}
		})
	}
	// A legacy event produced by a cancelled old decoder must replay ordinary
	// keys, including the bytes after confirmation, through the shared decoder.
	in := strings.NewReader(" \r \r")
	old := &termIO{in: in, input: sharedInputState(in)}
	read, _ := old.startRead(t.Context())
	<-read.done
	first, err := Multichoice(t.Context(), "First", []string{"one"}, WithInput(in), WithOutput(io.Discard))
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	second, err := Multichoice(t.Context(), "Second", []string{"two"}, WithInput(in), WithOutput(io.Discard))
	if err != nil || len(second) != 1 {
		t.Fatal(second, err)
	}
}

func TestMultichoiceCancelledPasteDiscardsPartialUpdate(t *testing.T) {
	input, typing := io.Pipe()
	defer func() { assert.NoError(t, input.Close()) }()
	defer func() { assert.NoError(t, typing.Close()) }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	in := newPromptReadNotifier(input)
	m := newPipeMultichoice(ctx, in)
	m.out = &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- m.run(m.Ctx) }()
	waitPromptRead(t, in)
	_, err := typing.Write([]byte("\x1b[200~one"))
	if err != nil {
		t.Fatal(err)
	}
	// Wait until the opening frame is decoded and the payload read is blocked.
	waitPromptRead(t, in)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	if m.typed != "" {
		t.Fatal("partial payload applied")
	}
	next := newPipeMultichoice(t.Context(), in)
	next.Items = []any{"next"}
	next.out = io.Discard
	go func() { done <- next.run(next.Ctx) }()
	_, err = typing.Write([]byte("\x1b[201~ \r"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("handoff blocked")
	}
	if !reflect.DeepEqual(next.indices(), []int{0}) || next.typed != "" {
		t.Fatal(next.indices(), next.typed)
	}
}

func TestMultichoiceLazyDefaultState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		items   []any
		hint    string
		toggles []int
		want    []int
		err     error
	}{
		{"unique", []any{"other", "api"}, "api", nil, []int{1}, nil},
		{"ambiguous later", []any{"api", "api server"}, "api", nil, []int{}, nil},
		{"duplicate", []any{"api", "api"}, "api", nil, []int{}, nil},
		{"manually unchecked", []any{"api", "other"}, "api", []int{0, 0}, []int{}, nil},
		{"already checked", []any{"api", "other"}, "api", []int{0}, []int{0}, nil},
		{"empty", nil, "api", nil, nil, ErrEmptyLazyResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMultichoice(t.Context())
			m.loading = true
			m.hint = tc.hint
			if err := m.prepare(nil); err != nil {
				t.Fatal(err)
			}
			for _, item := range tc.items {
				if err := m.addItem(item); err != nil {
					t.Fatal(err)
				}
				if m.count != 0 {
					t.Fatal("provisional default")
				}
			}
			for _, i := range tc.toggles {
				m.setFocus(i)
				m.handleKey(' ')
			}
			m.filter("zzz")
			if m.handleKey(keyEnter) {
				t.Fatal("loading confirmed")
			}
			err := m.finishLoading()
			if !errors.Is(err, tc.err) {
				t.Fatal(err)
			}
			if err == nil && !reflect.DeepEqual(m.indices(), tc.want) {
				t.Fatal(m.indices(), tc.want)
			}
		})
	}
}

func TestMultichoiceLazyArrivalsAndFocus(t *testing.T) {
	m := newMultichoice(t.Context())
	m.loading = true
	m.hint = "cat"
	if err := m.prepare(nil); err != nil {
		t.Fatal(err)
	}
	for _, item := range []string{"xxxx", "yyyy", "bat"} {
		if err := m.addItem(item); err != nil {
			t.Fatal(err)
		}
	}
	if m.focused() != 2 {
		t.Fatal("focus did not follow best-ranked arrival")
	}
	m.handleKey('↓')
	focused := m.focused()
	if err := m.addItem("cat"); err != nil {
		t.Fatal(err)
	}
	if m.focused() != focused {
		t.Fatal("arrival changed manual focus")
	}
	m.filter("z")
	if err := m.addItem("zoo"); err != nil {
		t.Fatal(err)
	}
	if len(m.relevant) != 1 || m.focused() != 4 {
		t.Fatal(m.relevant)
	}
	if err := m.finishLoading(); err != nil {
		t.Fatal(err)
	}
	if m.focused() != 4 || !reflect.DeepEqual(m.indices(), []int{3}) {
		t.Fatal(m.focused(), m.indices())
	}
}

func TestMultichoiceFilePendingKeysReachFreshPrompt(t *testing.T) {
	input, typing, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { assert.NoError(t, input.Close()) }()
	defer func() { assert.NoError(t, typing.Close()) }()
	_, err = typing.WriteString(" \r \r")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		m := newPipeMultichoice(t.Context(), input)
		done := make(chan error, 1)
		go func() { done <- m.run(m.Ctx) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("lost pending file keys")
		}
		if m.count != 1 {
			t.Fatal(m.count)
		}
	}
}

type countedChecklistReader struct {
	reader io.Reader
	reads  int
}

// Read counts source access to detect reads started after confirmation.
func (r *countedChecklistReader) Read(p []byte) (int, error) { r.reads++; return r.reader.Read(p) }

func TestMultichoiceNoReadAfterEnterOrConsumedReplay(t *testing.T) {
	in := &countedChecklistReader{reader: strings.NewReader(" \r")}
	got, err := Multichoice(t.Context(), "Pick", []string{"one"}, WithInput(in), WithOutput(io.Discard))
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	if in.reads != 1 {
		t.Fatal("read started after Enter", in.reads)
	}
	got, err = Multichoice(t.Context(), "Next", []string{"two"}, WithInput(in), WithOutput(io.Discard))
	if !errors.Is(err, io.EOF) || got != nil {
		t.Fatal("consumed keys replayed", got, err)
	}
	inputStates.Lock()
	_, retained := inputStates.states[in]
	inputStates.Unlock()
	if retained {
		t.Fatal("EOF retained reader")
	}
}

func TestMultichoiceEnterDecodedBeforeExhaustionCannotConfirm(t *testing.T) {
	m := newMultichoice(t.Context())
	m.loading = true
	if err := m.prepare(nil); err != nil {
		t.Fatal(err)
	}
	if err := m.addItem("one"); err != nil {
		t.Fatal(err)
	}
	if err := m.finishLoading(); err != nil {
		t.Fatal(err)
	}
	done, err := m.acceptEvent(&termIO{}, keyEvent{key: keyEnter, readOrder: m.readyOrder - 1})
	if done || err != nil {
		t.Fatal(done, err)
	}
	done, err = m.acceptEvent(&termIO{}, keyEvent{key: keyEnter, readOrder: m.readyOrder})
	if !done || err != nil {
		t.Fatal(done, err)
	}
}

func TestMultichoiceIndependentReaders(t *testing.T) {
	firstInput, typing := io.Pipe()
	defer func() { assert.NoError(t, firstInput.Close()) }()
	defer func() { assert.NoError(t, typing.Close()) }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := newPipeMultichoice(ctx, firstInput)
	out := &syncBuffer{}
	first.out = out
	done := make(chan error, 1)
	go func() { done <- first.run(first.Ctx) }()
	waitBufferContains(t, out, "[ ] one")
	got, err := Multichoice(
		t.Context(),
		"Independent",
		[]string{"two"},
		WithInput(strings.NewReader(" \r")),
		WithOutput(io.Discard),
	)
	if err != nil || !reflect.DeepEqual(got, []string{"two"}) {
		t.Fatal(got, err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first prompt did not cancel")
	}
	if first.count != 0 {
		t.Fatal("selection crossed readers")
	}
}

type choiceTemplateFailure struct{ err error }

// Label exposes a sentinel error through template execution.
func (x choiceTemplateFailure) Label() (string, error) { return "", x.err }

func TestMultichoiceTemplateErrorPreservesUnderlyingCause(t *testing.T) {
	cause := errors.New("label failed")
	eager, err := Multichoice(
		t.Context(),
		"Eager",
		[]choiceTemplateFailure{{cause}},
		WithTemplate(".Label"),
		WithOutput(io.Discard),
	)
	if eager != nil || !errors.Is(err, cause) {
		t.Fatal(eager, err)
	}
	input, typing := io.Pipe()
	defer func() { assert.NoError(t, input.Close()) }()
	defer func() { assert.NoError(t, typing.Close()) }()
	seq := func(yield func(choiceTemplateFailure, error) bool) { yield(choiceTemplateFailure{cause}, nil) }
	lazy, err := MultichoiceLazy(
		t.Context(),
		"Lazy",
		seq,
		WithTemplate(".Label"),
		WithInput(input),
		WithOutput(io.Discard),
	)
	if lazy != nil || !errors.Is(err, cause) {
		t.Fatal(lazy, err)
	}
}

func TestMultichoiceLazyPublicLoading(t *testing.T) {
	input, typing := io.Pipe()
	defer func() { assert.NoError(t, input.Close()) }()
	defer func() { assert.NoError(t, typing.Close()) }()
	arrivals := make(chan string)
	out := &syncBuffer{}
	seq := func(yield func(string, error) bool) {
		for item := range arrivals {
			if !yield(item, nil) {
				return
			}
		}
	}
	result := make(chan []string, 1)
	failure := make(chan error, 1)
	go func() {
		got, err := MultichoiceLazy(t.Context(), "Lazy", seq, WithDefault("api"), WithInput(input), WithOutput(out))
		result <- got
		failure <- err
	}()
	waitBufferContains(t, out, "Lazy")
	arrivals <- "api"
	waitBufferContains(t, out, "[ ] api")
	if _, err := typing.Write([]byte{'\r'}); err != nil {
		t.Fatal(err)
	}
	if _, err := typing.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}
	waitBufferContains(t, out, "No matches")
	if _, err := typing.Write([]byte{0x7f}); err != nil {
		t.Fatal(err)
	}
	waitBufferContains(t, out, "[ ] api")
	arrivals <- "worker"
	close(arrivals)
	waitBufferContains(t, out, "[x] api")
	select {
	case err := <-failure:
		t.Fatalf("queued Enter confirmed after loading: %v", err)
	default:
	}
	if _, err := typing.Write([]byte{'\r'}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failure:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh Enter failed")
	}
	if got := <-result; !reflect.DeepEqual(got, []string{"api"}) {
		t.Fatal(got)
	}
}

func TestMultichoiceLazyPublicFailures(t *testing.T) {
	sentinel := errors.New("producer failed")
	for _, tc := range []struct {
		name string
		seq  func(func(string, error) bool)
		want error
	}{
		{"empty", func(func(string, error) bool) {}, ErrEmptyLazyResult},
		{"error only", func(yield func(string, error) bool) { yield("", sentinel) }, sentinel},
		{"late error", func(yield func(string, error) bool) {
			if yield("api", nil) {
				yield("", sentinel)
			}
		}, sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			input, typing := io.Pipe()
			defer func() { assert.NoError(t, input.Close()) }()
			defer func() { assert.NoError(t, typing.Close()) }()
			got, err := MultichoiceLazy(ctx, "Lazy", tc.seq, WithInput(input), WithOutput(io.Discard))
			if got != nil || !errors.Is(err, tc.want) {
				t.Fatal(got, err)
			}
		})
	}
	got, err := MultichoiceLazy[string](t.Context(), "Nil", nil)
	if got != nil || !errors.Is(err, ErrInvalidOption) {
		t.Fatal(got, err)
	}
}

func TestMultichoiceProducerStopsBlockedPublication(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan struct{})
	started := make(chan struct{})
	m := newMultichoice(t.Context())
	m.loading = true
	m.producer = func(yield func(any, error) bool) {
		defer close(stopped)
		close(started)
		for i := 0; ; i++ {
			if !yield(i, nil) {
				return
			}
		}
	}
	publication := m.startProducer(ctx)
	<-started
	// No consumer can drain this bounded queue; cancellation must unblock yield.
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("yield did not return false")
	}
	if len(publication) > 16 {
		t.Fatal("unbounded queue")
	}
}

func TestMultichoiceStaticAndLazyLargeLists(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	items := make([]string, 10000)
	for i := range items {
		items[i] = fmt.Sprintf("item-%05d", i)
	}
	got, err := Multichoice(
		ctx,
		"Large",
		items,
		WithDefault("item-09999"),
		WithInput(strings.NewReader("\r")),
		WithOutput(io.Discard),
	)
	if err != nil || !reflect.DeepEqual(got, []string{"item-09999"}) {
		t.Fatal(got, err)
	}
	m := newMultichoice(ctx)
	m.loading = true
	m.producer = func(yield func(any, error) bool) {
		for _, item := range items {
			if !yield(item, nil) {
				return
			}
		}
	}
	if err := m.prepare(nil); err != nil {
		t.Fatal(err)
	}
	publication := m.startProducer(ctx)
	for pair := range publication {
		if err := m.addItem(pair.item); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.finishLoading(); err != nil {
		t.Fatal(err)
	}
	if len(m.Items) != len(items) || len(m.relevant) != len(items) {
		t.Fatal("lost burst arrivals")
	}
}

func TestMultichoiceManagedCleanupAndOutputFailure(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(strconv.FormatBool(short), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cio := newUnstartedIO(ctx, 40, 8, 0)
			sentinel := errors.New("terminal failed")
			go cio.handleViewports(ctx)
			go cio.forwardTo(ctx, choiceFailWriter{err: sentinel, short: short})
			m := newMultichoice(t.Context())
			m.Items = []any{"one"}
			m.in = strings.NewReader("\r")
			m.out = cio
			var vp *viewport
			m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
				var err error
				vp, err = cio.pushViewport()
				if err != nil {
					return nil, err
				}
				return withRestore(
					&termIO{in: in, out: out, Width: 40, Height: 8, cio: cio, vp: vp},
					func() error { return nil },
				), nil
			}
			err := m.run(m.Ctx)
			want := sentinel
			if short {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
			cio.chainMu.Lock()
			released := true
			for current := cio.head; current != nil; current = current.next {
				if current == vp {
					released = false
				}
			}
			cio.chainMu.Unlock()
			if !released {
				t.Fatal("viewport not released")
			}
			cancel()
			<-cio.done
		})
	}
}

func TestMultichoicePasteModeLifetime(t *testing.T) {
	original := terminalInputChecker
	terminalInputChecker = func(int) bool { return true }
	t.Cleanup(func() { terminalInputChecker = original })
	for _, keys := range []string{"\r", "\x03"} {
		var out strings.Builder
		in := &mockDescriptor{Reader: strings.NewReader(keys), fd: 1}
		m := newMultichoice(t.Context())
		m.Items = []any{"one"}
		m.in = in
		m.out = &out
		restores := 0
		m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			return &termIO{
				in:      in,
				out:     out,
				Width:   40,
				Height:  8,
				Restore: func() error { restores++; return nil },
			}, nil
		}
		err := m.run(m.Ctx)
		if keys == "\r" && err != nil {
			t.Fatal(err)
		}
		if keys != "\r" && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if restores != 1 || strings.Count(out.String(), "\x1b[?2004h") != 1 ||
			strings.Count(out.String(), "\x1b[?2004l") != 1 {
			t.Fatal(out.String(), restores)
		}
	}
}

// completionChecklistIO waits for the unique default to be checked after
// exhaustion, which a frame shows with marker, "[x]" when empty.
type completionChecklistIO struct {
	ready  chan struct{}
	sent   bool
	keys   *strings.Reader
	marker string
}

// Write releases input once the exhausted sequence renders its checked default.
func (s *completionChecklistIO) Write(p []byte) (int, error) {
	marker := s.marker
	if marker == "" {
		marker = "[x]"
	}
	if !s.sent && strings.Contains(string(p), marker) {
		s.sent = true
		close(s.ready)
	}
	return len(p), nil
}

// Read waits for the checked default before supplying confirmation keys.
func (s *completionChecklistIO) Read(p []byte) (int, error) {
	<-s.ready
	if s.keys != nil {
		return s.keys.Read(p)
	}
	p[0] = keyEnter
	return 1, nil
}

func TestMultichoiceBurstyLazyPublicSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	seq := func(yield func(int, error) bool) {
		for i := range 2000 {
			if !yield(i, nil) {
				return
			}
		}
	}
	stream := &completionChecklistIO{ready: make(chan struct{})}
	got, err := MultichoiceLazy(
		ctx,
		"Burst",
		seq,
		WithDefault("1999"),
		WithInput(stream),
		WithOutput(stream),
		WithHide(),
	)
	if err != nil || !reflect.DeepEqual(got, []int{1999}) {
		t.Fatal(got, err)
	}
}

func TestMultichoiceLazyDuplicateAndNilProjection(t *testing.T) {
	stream := &completionChecklistIO{ready: make(chan struct{}), keys: strings.NewReader(" \x1b[B \x1b[B \r")}
	seq := func(yield func([]int, error) bool) {
		for _, item := range [][]int{{1}, {1}, {2}} {
			if !yield(item, nil) {
				return
			}
		}
	}
	got, err := MultichoiceLazy(
		t.Context(),
		"Equal",
		seq,
		WithDefault("[2]"),
		WithInput(stream),
		WithOutput(stream),
		WithHide(),
	)
	if err != nil || !reflect.DeepEqual(got, [][]int{{1}, {1}}) {
		t.Fatal(got, err)
	}
	stream = &completionChecklistIO{ready: make(chan struct{}), keys: strings.NewReader(" \x1b[B \x1b[B \r")}
	nils := []any{nil, (*int)(nil)}
	anySeq := func(yield func(any, error) bool) {
		for _, item := range append(nils, "ready") {
			if !yield(item, nil) {
				return
			}
		}
	}
	values, err := MultichoiceLazy(
		t.Context(),
		"Nil",
		anySeq,
		WithDefault("ready"),
		WithInput(stream),
		WithOutput(stream),
		WithHide(),
	)
	if err != nil || !reflect.DeepEqual(values, nils) {
		t.Fatal(values, err)
	}
}

// promptReadNotifier exposes source-read boundaries without changing buffering.
type promptReadNotifier struct {
	io.Reader
	reads chan struct{}
}

// newPromptReadNotifier exposes read boundaries for deterministic handoff tests.
func newPromptReadNotifier(in io.Reader) *promptReadNotifier {
	return &promptReadNotifier{Reader: in, reads: make(chan struct{}, 16)}
}

// Read signals source access before blocking in the wrapped reader.
func (r *promptReadNotifier) Read(p []byte) (int, error) {
	r.reads <- struct{}{}
	return r.Reader.Read(p)
}

// waitPromptRead synchronizes tests at source-read boundaries.
func waitPromptRead(t *testing.T, in *promptReadNotifier) {
	t.Helper()
	select {
	case <-in.reads:
	case <-time.After(time.Second):
		t.Fatal("prompt did not reach the next source read")
	}
}

// cancelPromptRead leaves a blocked public-prompt read for its successor.
func cancelPromptRead(t *testing.T, in *promptReadNotifier, run func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	waitPromptRead(t, in)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled prompt did not return")
	}
}

type promptChoiceResult struct {
	items []string
	err   error
}

func TestMultichoiceLegacyHandoffPreservesPasteFraming(t *testing.T) {
	for _, payload := range []string{" ", " \r", " \x03", " \x04", "\r\x03\x04 "} {
		t.Run(strconv.Quote(payload), func(t *testing.T) {
			pipe, typing := io.Pipe()
			defer func() { assert.NoError(t, pipe.Close()) }()
			defer func() { assert.NoError(t, typing.Close()) }()
			in := newPromptReadNotifier(pipe)
			cancelPromptRead(t, in, func(ctx context.Context) error {
				_, err := Input(ctx, "Old", WithInput(in), WithOutput(io.Discard))
				return err
			})
			ctx, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			out := &syncBuffer{}
			done := make(chan promptChoiceResult, 1)
			go func() {
				items, err := Multichoice(ctx, "Next", []string{"one", "two"}, WithInput(in), WithOutput(out))
				done <- promptChoiceResult{items, err}
			}()
			waitBufferContains(t, out, "[ ] one")
			if _, err := typing.Write([]byte("\x1b[200~" + payload + "\x1b[201~")); err != nil {
				t.Fatal(err)
			}
			// A new read proves the entire paste was accepted without confirming
			// or treating Ctrl-C/D as EOF; no timing delay is needed.
			select {
			case got := <-done:
				t.Fatalf("paste ended the prompt: %v, %v", got.items, got.err)
			case <-in.reads:
			case <-ctx.Done():
				t.Fatal("paste did not reach the successor")
			}
			if _, err := typing.Write([]byte("\r")); err != nil {
				t.Fatal(err)
			}
			got := <-done
			if got.err != nil || got.items == nil || len(got.items) != 0 {
				t.Fatalf("pasted Space checked an item: %v, %v", got.items, got.err)
			}
		})
	}
}

func TestMultichoiceFreshPasteAfterCancelledRead(t *testing.T) {
	pipe, typing := io.Pipe()
	defer func() { assert.NoError(t, pipe.Close()) }()
	defer func() { assert.NoError(t, typing.Close()) }()
	in := newPromptReadNotifier(pipe)
	cancelPromptRead(t, in, func(ctx context.Context) error {
		_, err := Multichoice(ctx, "Old", []string{"old"}, WithInput(in), WithOutput(io.Discard))
		return err
	})
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	out := &syncBuffer{}
	done := make(chan promptChoiceResult, 1)
	go func() {
		items, err := Multichoice(ctx, "Next", []string{"one", "two"}, WithInput(in), WithOutput(out))
		done <- promptChoiceResult{items, err}
	}()
	waitBufferContains(t, out, "[ ] one")
	if _, err := typing.Write([]byte("\x1b[200~two\x1b[201~ \r")); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || !reflect.DeepEqual(got.items, []string{"two"}) {
		t.Fatalf("fresh paste was discarded: %v, %v", got.items, got.err)
	}
}

func TestMultichoiceBufferedEnterRetainsArrival(t *testing.T) {
	for _, tc := range []struct {
		name     string
		split    bool
		buffered int
	}{
		{"legacy replay", false, 2},
		{"pending bytes", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMultichoice(t.Context())
			m.loading = true
			if err := m.prepare(nil); err != nil {
				t.Fatal(err)
			}
			if err := m.addItem("one"); err != nil {
				t.Fatal(err)
			}
			in := &chunkReader{chunks: [][]byte{[]byte("\t\r"), []byte("\r")}}
			old := &termIO{in: in, splitKeys: tc.split}
			read, accept := old.startRead(t.Context())
			<-read.done
			accept()
			if read.event.readOrder == 0 {
				t.Fatal("source read has no arrival order")
			}
			if err := m.finishLoading(); err != nil {
				t.Fatal(err)
			}
			if !tc.split {
				if done, err := m.acceptEvent(old, read.event); done || err != nil {
					t.Fatal(done, err)
				}
			}
			// A fresh termIO must preserve both pending and replayed arrival orders.
			next := &termIO{in: in, splitKeys: true, bracketedPaste: true}
			for range tc.buffered {
				buffered, consume := next.startRead(t.Context())
				<-buffered.done
				consume()
				if buffered.event.readOrder != read.event.readOrder {
					t.Fatal("buffered bytes lost their source-read order")
				}
				if done, err := m.acceptEvent(next, buffered.event); done || err != nil {
					t.Fatalf("old Enter confirmed after loading: %v, %v", done, err)
				}
			}
			fresh, consume := next.startRead(t.Context())
			<-fresh.done
			consume()
			if fresh.event.readOrder <= m.readyOrder {
				t.Fatal("fresh Enter retained an old arrival order")
			}
			if done, err := m.acceptEvent(next, fresh.event); !done || err != nil {
				t.Fatalf("fresh Enter did not confirm: %v, %v", done, err)
			}
		})
	}
}

func TestTermIOPasteTailRetainsFreshArrival(t *testing.T) {
	pipe, typing := io.Pipe()
	defer func() { assert.NoError(t, pipe.Close()) }()
	defer func() { assert.NoError(t, typing.Close()) }()
	in := newPromptReadNotifier(pipe)
	old := &termIO{in: in, splitKeys: true, bracketedPaste: true}
	read, accept := old.startRead(t.Context())
	waitPromptRead(t, in)
	if _, err := typing.Write([]byte("\x1b[200~one")); err != nil {
		t.Fatal(err)
	}
	// The decoder has observed the opening frame and is waiting for its end.
	waitPromptRead(t, in)
	readyOrder := nextInputOrder()
	if _, err := typing.Write([]byte("\x1b[201~\r")); err != nil {
		t.Fatal(err)
	}
	<-read.done
	accept()
	if !read.event.bracketed || read.event.readOrder >= readyOrder {
		t.Fatal("paste lost the arrival order of its opening frame")
	}
	next := &termIO{in: in, splitKeys: true, bracketedPaste: true}
	tail, consume := next.startRead(t.Context())
	<-tail.done
	consume()
	if tail.event.key != keyEnter || tail.event.readOrder <= readyOrder {
		t.Fatal("Enter after the closing frame retained the paste's old arrival order")
	}
}

// acquireDefaultMultichoice inspects stream routing at terminal acquisition, after validation.
func acquireDefaultMultichoice(t *testing.T) *multichoice {
	t.Helper()
	m := newMultichoice(t.Context())
	m.Items = []any{"one"}
	m.makeTermIO = func(io.Reader, io.Writer) (*termIO, error) { return nil, ErrNoTTY }
	assert.ErrorIs(t, m.run(m.Ctx), ErrNoTTY)
	return m
}

func TestMultichoiceDefaultStreams(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(strconv.FormatBool(tty), func(t *testing.T) {
			prevIn, prevOut := defaultIO.input(), defaultIO.rawOutput()
			prevTTY, prevSize := terminalWriterChecker, termGetSize
			t.Cleanup(func() {
				terminalWriterChecker, termGetSize = prevTTY, prevSize
				SetDefaultIO(prevIn, prevOut)
			})
			in := bytes.NewBufferString("in")
			var out io.Writer = &bytes.Buffer{}
			if tty {
				out = &mockDescriptor{Writer: &bytes.Buffer{}, fd: 7}
			}
			terminalWriterChecker = func(int) bool { return tty }
			termGetSize = func(int) (int, int, error) { return 80, 24, nil }
			SetDefaultIO(in, out)
			wantOut := out
			if tty {
				_ = Stderr()
				_, wantOut = defaultStreams(t.Context())
				cio, ok := wantOut.(*chanIO)
				assert.True(t, ok)
				assert.NotNil(t, cio)
			}
			m := acquireDefaultMultichoice(t)
			assert.Equal(t, in, m.in)
			assert.Equal(t, wantOut, m.out)
		})
	}
}

func TestExplicitAnswerTemplateSurvivesContentTemplates(t *testing.T) {
	for name, content := range map[string]opt{"template": WithTemplate(".Name"), "field": WithFieldTemplate("Name")} {
		for _, answerFirst := range []bool{true, false} {
			m := newMultichoice(t.Context())
			o := []opt{WithAnswerTemplate("custom"), content}
			if !answerFirst {
				o = []opt{content, WithAnswerTemplate("custom")}
			}
			if err := opts(o).Apply(m); err != nil {
				t.Fatal(err)
			}
			if m.AnswerTemplate != "custom" {
				t.Fatalf("%s answerFirst=%v: %q", name, answerFirst, m.AnswerTemplate)
			}
		}
	}
}

func TestMultichoiceDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()
	items := []string{"a", "b", "c", "d"}

	got, err := Multichoice(ctx, "Pick", items, io, WithDefault("b"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"b"}, got)

	// source order, not default order
	got, err = Multichoice(ctx, "Pick", items, io, WithDefault([]string{"d", "b"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"b", "d"}, got)

	got, err = Multichoice(ctx, "Pick", items, io, WithDefault([]string{}))
	assert.NoError(t, err)
	assert.Equal(t, []string{}, got)
	assert.True(t, got != nil)

	got, err = Multichoice(ctx, "Pick", items, io, WithDefault([]string(nil)))
	assert.NoError(t, err)
	assert.True(t, got != nil && len(got) == 0)

	for _, bad := range []any{[]string{"x"}, []string{"a", "a"}, []int{1}, 5} {
		_, err = Multichoice(ctx, "Pick", items, io, WithDefault(bad))
		assert.ErrorIs(t, err, ErrInvalidOption)
	}
	_, err = Multichoice(ctx, "Pick", []string{"a", "a"}, io, WithDefault([]string{"a"}))
	assert.ErrorIs(t, err, ErrInvalidOption)

	// a sole item is not consent
	_, err = Multichoice(ctx, "Pick", []string{"a"}, io)
	assert.ErrorIs(t, err, ErrNoTTY)
	_, err = Multichoice(ctx, "Pick", []string{"a"}, io, WithOneReturn())
	assert.ErrorIs(t, err, ErrWrongWidget)

	// when T is a slice, the whole value is one item first
	groups := [][]string{{"a"}, {"b", "c"}}
	grp, err := Multichoice(ctx, "Pick", groups, io, WithDefault([]string{"b", "c"}))
	assert.NoError(t, err)
	assert.Equal(t, [][]string{{"b", "c"}}, grp)
	grp, err = Multichoice(ctx, "Pick", groups, io, WithDefault([][]string{{"b", "c"}, {"a"}}))
	assert.NoError(t, err)
	assert.Equal(t, groups, grp)

	structs := []server{{"alpha", 1}, {"beta", 2}}
	st, err := Multichoice(ctx, "Pick", structs, io, WithDefault(server{"beta", 2}))
	assert.NoError(t, err)
	assert.Equal(t, structs[1:], st)
}

func TestMultichoiceMapDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()
	items := map[string]int{"one": 1, "two": 2, "three": 3}

	keys, err := MultichoiceKVK(ctx, "Pick", items, io, WithDefault([]string{"two", "one"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, keys)

	values, err := MultichoiceKVV(ctx, "Pick", items, io, WithDefault([]int{3, 1}))
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 3}, values)

	value, err := MultichoiceKVV(ctx, "Pick", items, io, WithDefault(2))
	assert.NoError(t, err)
	assert.Equal(t, []int{2}, value)

	// the API decides the projection when K == V
	same := map[int]int{1: 2, 2: 1}
	k, err := MultichoiceKVK(ctx, "Pick", same, io, WithDefault(1))
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, k)
	v, err := MultichoiceKVV(ctx, "Pick", same, io, WithDefault(1))
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, v)

	_, err = MultichoiceKVV(ctx, "Pick", map[string]int{"a": 1, "b": 1}, io, WithDefault(1))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = MultichoiceKVK(ctx, "Pick", items, io, WithDefault([]int{1}))
	assert.ErrorIs(t, err, ErrInvalidOption)
}

func TestMultichoiceLazyDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()
	var started bool

	_, err := MultichoiceLazy(ctx, "Pick", seq(&started, nil, "a"), io)
	assert.ErrorIs(t, err, ErrNoTTY)
	assert.True(t, !started)

	got, err := MultichoiceLazy(ctx, "Pick", seq(&started, nil, "a", "b", "c"), io, WithDefault([]string{"c", "a"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, got)

	_, err = MultichoiceLazy(ctx, "Pick", seq(&started, nil, "a", "a"), io, WithDefault([]string{"a"}))
	assert.ErrorIs(t, err, ErrInvalidOption)

	late := errors.New("late")
	_, err = MultichoiceLazy(ctx, "Pick", seq(&started, late, "a"), io, WithDefault("a"))
	assert.ErrorIs(t, err, late)

	_, err = MultichoiceLazy(ctx, "Pick", seq[string](&started, nil), io, WithDefault("a"))
	assert.ErrorIs(t, err, ErrEmptyLazyResult)
}

func TestTypedDefaultChecksInteractiveMultichoice(t *testing.T) {
	var out bytes.Buffer
	got, err := Multichoice(t.Context(), "Pick", []string{"a", "b", "c"},
		WithInput(strings.NewReader("\r")), WithOutput(&out), WithDefault([]string{"c", "a"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, got)
	assert.Contains(t, out.String(), "[x] c")
}

func TestMultichoiceLazyAutomaticAnswerCancelsWhileProducerBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})
	defer close(release)
	seq := func(_ func(string, error) bool) {
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, err := MultichoiceLazy(ctx, "Pick", seq, WithDefault("a"), WithOutput(&bytes.Buffer{}))
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

// themedMultichoiceOpt routes a themed multichoice through a 60-column scripted terminal.
func themedMultichoiceOpt(t *testing.T, th Theme, keys ...string) (o opt, out *bytes.Buffer, restored *bool) {
	t.Helper()
	s := scripted(t, th, 60, keys...)
	return opT(func(m *multichoice) error {
		m.in, m.out, m.makeTermIO = s.in, s.out, s.makeTermIO
		return nil
	}), s.out, s.restored
}

// boundMultichoice prepares a themed prompt for frame tests at the given terminal height.
func boundMultichoice(t *testing.T, th Theme, height int, items []any, o ...opt) (*multichoice, *termIO) {
	t.Helper()
	useTheme(t, th)
	m := newMultichoice(t.Context())
	m.themed = true
	m.Label, m.Items = "Fruit", items
	assert.NoError(t, m.prepare(o))
	tt := &termIO{out: &bytes.Buffer{}, Width: 60, Height: height}
	assert.NoError(t, m.bindRun(t.Context(), tt))
	return m, tt
}

// mframe draws one frame of m on tt.
func mframe(t *testing.T, m *multichoice, tt *termIO) string {
	t.Helper()
	var buf bytes.Buffer
	assert.NoError(t, m.render(tt, &buf))
	return buf.String()
}

const multichoiceHelp = "| up/down move · space toggle · type filter\n\r"

func TestThemedMultichoiceGuidedFrameStatesAndAnswer(t *testing.T) {
	o, out, restored := themedMultichoiceOpt(t, asciiTheme(false), " ", "\x1b[B", " ", "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"apple", "banana"}, got)
	assert.True(t, *restored)
	s := out.String()
	// the label has its own row; the rows and the hint are behind the guide
	assert.True(t, strings.HasPrefix(s, ""+
		"\r? Fruit\n\r| > o apple\n\r|   o banana\n\r|   o cherry\n\r"+multichoiceHelp))
	// focused and checked, each marked exactly once
	assert.Contains(t, s, "\r? Fruit\n\r| > x apple\n\r|   o banana\n")
	assert.Contains(t, s, "\r? Fruit\n\r|   x apple\n\r| > o banana\n\r|   o cherry\n")
	assert.Contains(t, s, "\r? Fruit\n\r|   x apple\n\r| > x banana\n\r|   o cherry\n")
	// confirming erases the five rows once and retains the collection
	erase := "\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"
	assert.True(t, strings.HasSuffix(s, erase+"+ Fruit · apple, banana\n"))
}

func TestThemedMultichoiceEmptySelectionAnswersNone(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{}, got)
	assert.True(t, strings.HasSuffix(out.String(), "\r+ Fruit · none\n"))
}

func TestThemedMultichoiceInlineHasNoGuide(t *testing.T) {
	m, tt := boundMultichoice(t, asciiTheme(true), 10, []any{"apple", "banana"})
	m.handleKey(' ')
	assert.Equal(t,
		"\r? Fruit\n\r> x apple\n\r  o banana\n\rup/down move · space toggle · type filter\n\r",
		mframe(t, m, tt))
}

func TestThemedMultichoiceCheckedHiddenItemsStayInSourceOrder(t *testing.T) {
	// check cherry, then apple after filtering cherry out of view
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false),
		"\x1b[B", "\x1b[B", " ", "a", " ", "\x7f", "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"apple", "cherry"}, got)
	s := out.String()
	assert.Contains(t, s, "\r? Fruit\n\r| > x apple\n\r"+multichoiceHelp)
	assert.True(t, strings.HasSuffix(s, "+ Fruit · apple, cherry\n"))
}

func TestThemedMultichoiceEmptyState(t *testing.T) {
	m, tt := boundMultichoice(t, asciiTheme(false), 10, []any{"apple", "banana"})
	m.handleKey(' ')
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r| No matches\n\r"+multichoiceHelp, mframe(t, m, tt))

	th := asciiTheme(false)
	th.Templates.Choice.Empty = `{{ printf "nothing for %q" .Query }}`
	m, tt = boundMultichoice(t, th, 10, []any{"apple"})
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r| nothing for \"zz\"\n\r"+multichoiceHelp, mframe(t, m, tt))

	// a hidden empty state reserves no row; the checked item still answers
	th.Templates.Multichoice.Empty = Hidden
	m, tt = boundMultichoice(t, th, 10, []any{"apple"})
	m.handleKey(' ')
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r"+multichoiceHelp, mframe(t, m, tt))
	assert.Equal(t, []int{0}, m.indices())
}

func TestThemedMultichoiceLoadingShowsNoEmptyState(t *testing.T) {
	useTheme(t, asciiTheme(false))
	m := newMultichoice(t.Context())
	m.themed, m.loading, m.Label = true, true, "Fruit"
	assert.NoError(t, m.prepare(nil))
	tt := &termIO{out: &bytes.Buffer{}, Width: 60, Height: 10}
	assert.NoError(t, m.bindRun(t.Context(), tt))
	// nothing has arrived yet, which is not an empty match
	assert.Equal(t, "\r? Fruit\n\r"+multichoiceHelp, mframe(t, m, tt))
	assert.NoError(t, m.addItem("apple"))
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r| No matches\n\r"+multichoiceHelp, mframe(t, m, tt))
}

func TestThemedMultichoiceMoreRowAndShortTerminals(t *testing.T) {
	items := []any{"a", "b", "c", "d", "e"}
	for _, tc := range []struct {
		height int
		want   string
	}{
		{10, "\r? Fruit\n\r| > o a\n\r|   o b\n\r|   o c\n\r|   o d\n\r|   o e\n\r" + multichoiceHelp},
		// three of five rows fit with the count of the rest; the cursor row
		// below the frame leaves no row for the hint, which goes first
		{6, "\r? Fruit\n\r| > o a\n\r|   o b\n\r|   o c\n\r| . 2 more\n\r"},
		// then the count, then the label
		{4, "\r? Fruit\n\r| > o a\n\r|   o b\n\r"},
		{3, "\r? Fruit\n\r| > o a\n\r"},
	} {
		t.Run(strconv.Itoa(tc.height), func(t *testing.T) {
			m, tt := boundMultichoice(t, asciiTheme(false), tc.height, items)
			assert.Equal(t, tc.want, mframe(t, m, tt))
		})
	}
	m, tt := boundMultichoice(t, asciiTheme(false), 6, items)
	for range 4 {
		m.handleKey('↓')
	}
	// at the bottom nothing is below, and the count row is gone
	assert.Equal(t, "\r? Fruit\n\r|   o c\n\r|   o d\n\r| > o e\n\r"+multichoiceHelp, mframe(t, m, tt))
}

func TestThemedMultichoiceProjectionMatchesAndAnswers(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}, {"cherry", "blue"}}
	// "b" is the start of a detail too, but only the projected name matches
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "b", " ", "\r")
	got, err := Multichoice(t.Context(), "Fruit", items, o, WithTemplate(".Name", ".Color"))
	assert.NoError(t, err)
	assert.Equal(t, []themedFruit{items[1]}, got)
	s := out.String()
	// details are on the active row only
	assert.Contains(t, s, "\r| > o apple red\n\r|   o banana\n\r|   o cherry\n")
	assert.Contains(t, s, "\r? Fruit\n\r| > x banana yellow\n\r"+multichoiceHelp)
	assert.True(t, strings.HasSuffix(s, "+ Fruit · banana\n"))
}

func TestThemedMultichoiceFieldTemplateProjects(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}}
	m, tt := boundMultichoice(t, asciiTheme(false), 10, []any{items[0], items[1]}, WithFieldTemplate("Name", "Color"))
	assert.Equal(t,
		"\r? Fruit\n\r| > o apple, red\n\r|   o banana, yellow\n\r"+multichoiceHelp,
		mframe(t, m, tt))
}

func TestThemedMultichoiceExplicitRowsKeepMarksInEitherOrder(t *testing.T) {
	active := WithActiveItemTemplate(`* {{ . }}`)
	project := WithTemplate(`(printf "<%s>" .)`)
	for name, o := range map[string][]opt{
		"explicit first":   {active, project},
		"projection first": {project, active},
	} {
		t.Run(name, func(t *testing.T) {
			m, tt := boundMultichoice(t, asciiTheme(false), 10, []any{"apple", "banana"}, o...)
			m.handleKey(' ')
			// the explicit content takes the raw item; the row marks it once
			assert.Equal(t,
				"\r? Fruit\n\r| > x * apple\n\r|   o <banana>\n\r"+multichoiceHelp,
				mframe(t, m, tt))
			// the answer is the projection, not the explicit row
			assert.Equal(t, "<apple>", m.content[0].String())
		})
	}
}

func TestThemedMultichoiceExplicitAnswerKeepsRawPayload(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}}
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), " ", "\r")
	_, err := Multichoice(t.Context(), "Fruit", items, o, WithTemplate(".Name"),
		WithAnswerTemplate(`{{ .Label }}={{ range .Answer }}{{ .Name }}/{{ .Color }}{{ end }} #{{ .Count }}`))
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), "\rFruit=apple/red #1\n"))
}

func TestThemedMultichoiceHiddenAnswerRetainsNothing(t *testing.T) {
	erase := "\x1b[4A\r"
	th := asciiTheme(false)
	th.Templates.Multichoice.Answer = Hidden
	o, out, _ := themedMultichoiceOpt(t, th, " ", "\r")
	_, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), erase))

	o, out, _ = themedMultichoiceOpt(t, asciiTheme(false), " ", "\r")
	_, err = Multichoice(t.Context(), "Fruit", fruits, o, WithHide())
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), erase))
}

func TestThemedMultichoiceSlotPrecedence(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Choice.Inactive = `{{ printf "-%s" .Value }}`
	th.Templates.Multichoice.Active = `[{{ .Value }}]`
	th.Templates.Dropdown.Inactive = `dropdown only`
	th.Templates.Multichoice.Row = `{{ if .Selected }}+{{ else }}.{{ end }}{{ .Value }}#{{ .Index }}`
	th.Templates.Choice.More = Hidden
	th.Templates.Prompt.Hint = Hidden
	th.Templates.Multichoice.Label = `{{ .Label }}:`
	m, tt := boundMultichoice(t, th, 4, []any{"a", "b", "c", "d"})
	m.handleKey('↓')
	m.handleKey(' ')
	// shared and family edits apply, other families do not; hidden slots reserve no row
	assert.Equal(t, "\rFruit:\n\r| .-a#0\n\r| +[b]#1\n\r", mframe(t, m, tt))
}

func TestThemedMultichoiceStateSlotsSeeSelection(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Choice.Inactive = `{{ if .Selected }}{{ upper .Value }}{{ else }}{{ .Value }}{{ end }}`
	th.Funcs = template.FuncMap{"upper": strings.ToUpper}
	m, tt := boundMultichoice(t, th, 10, []any{"apple", "banana"})
	m.handleKey(' ')
	m.handleKey('↓')
	assert.Contains(t, mframe(t, m, tt), "|   x APPLE\n\r| > o banana\n")
}

func TestThemedMultichoiceAnswerValueAndSharedAnswer(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Prompt.Answer = `{{ .Label }}: {{ .Value }} ({{ .Count }})`
	th.Templates.Multichoice.AnswerValue = `{{ range .Values }}[{{ . }}]{{ end }}`
	o, out, _ := themedMultichoiceOpt(t, th, "\x1b[B", " ", "\x1b[B", " ", "\r")
	_, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), "\rFruit: [banana][cherry] (2)\n"))
}

func TestThemedMultichoicePerCallMoreAndLabelKeepPayloads(t *testing.T) {
	m, tt := boundMultichoice(t, asciiTheme(false), 5, []any{"a", "b", "c", "d"},
		WithMoreItemsTemplate(`{{ .More }}/{{ .Total }}`), WithLabelTemplate(`<{{ . }}>`))
	assert.Equal(t, "\r<Fruit>\n\r| > o a\n\r|   o b\n\r| 2/4\n\r", mframe(t, m, tt))
}

func TestThemedMultichoiceKVShowsKeys(t *testing.T) {
	items := map[string]int{"b": 2, "a": 1}
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\x1b[B", " ", "\r")
	keys, err := MultichoiceKVK(t.Context(), "Pick", items, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"b"}, keys)
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > o a\n\r|   o b\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · b\n"))

	o, out, _ = themedMultichoiceOpt(t, asciiTheme(false), " ", "\r")
	values, err := MultichoiceKVV(t.Context(), "Pick", items, o)
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, values)
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · a\n"))
}

func TestThemedMultichoiceTypedDefaultChecksAfterBinding(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\r")
	got, err := Multichoice(t.Context(), "Pick", []string{"a", "b", "c"}, o, WithDefault([]string{"c", "a"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, got)
	// the first checked entry takes the focus
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > x a\n\r|   o b\n\r|   x c\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · a, c\n"))

	o, _, restored := themedMultichoiceOpt(t, asciiTheme(false), "\r")
	_, err = Multichoice(t.Context(), "Pick", []string{"a"}, o, WithDefault([]string{"zz"}))
	assert.ErrorIs(t, err, ErrInvalidOption)
	assert.True(t, *restored)
}

func TestThemedMultichoiceHintMatchesProjection(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\r")
	got, err := Multichoice(t.Context(), "Pick", fruits, o, WithTemplate(`(printf "z%s" .)`), WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · zbanana\n"))
}

func TestThemedMultichoiceAutomaticAnswerRunsOnlyProjection(t *testing.T) {
	strictOutput(t)
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	// no presentation slot runs without a terminal: only the projection
	th.Templates.Prompt.Label = `{{ fails .Label }}`
	th.Templates.Choice.Active = `{{ fails .Value }}`
	th.Templates.Choice.Inactive = `{{ fails .Value }}`
	th.Templates.Multichoice.Row = `{{ fails .Value }}`
	th.Templates.Multichoice.AnswerValue = `{{ range .Values }}{{ fails . }}{{ end }}`
	th.Templates.Choice.Value = `{{ printf "z%s" . }}`
	useTheme(t, th)
	var out bytes.Buffer
	got, err := Multichoice(t.Context(), "Fruit", fruits, WithOutput(&out), WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)

	got, err = Multichoice(t.Context(), "Fruit", fruits, WithOutput(&out),
		WithTemplate(`(printf "y%s" .)`), WithDefault("yc"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"cherry"}, got)

	// a per-call inactive template without a projection is matched as before
	got, err = Multichoice(t.Context(), "Fruit", fruits, WithOutput(&out),
		WithInactiveItemTemplate(`x{{ . }}`), WithDefault("xa"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"apple"}, got)

	got, err = MultichoiceLazy(t.Context(), "Fruit", lazySeq("apple", "banana"), WithOutput(&out), WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)
	assert.Equal(t, "", out.String())
}

func TestThemedMultichoiceSearchSkipsDecoration(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Choice.Active = `{{ .Value }} zzz`
	th.Templates.Choice.Inactive = `{{ .Value }} zzz`
	th.Templates.Multichoice.Row = `zzz {{ .Value }}`
	m, tt := boundMultichoice(t, th, 10, []any{"apple", "banana"})
	mframe(t, m, tt)
	// the presentation of the rows is not searchable, only their content
	assert.Equal(t, 0, len(m.trie.Prefix("zzz")))
	assert.Equal(t, []int{1}, m.trie.Prefix("b"))

	// a late item is projected and matched like the others
	assert.NoError(t, m.addItem("blueberry"))
	assert.Equal(t, []int{1, 2}, m.trie.Prefix("b"))
	assert.Equal(t, "blueberry", m.content[2].String())
}

func TestThemedMultichoiceProjectsOnceAndDrawsLazily(t *testing.T) {
	calls := map[string]int{}
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"count": func(kind, s string) string { calls[kind+":"+s]++; return s }}
	th.Templates.Choice.Value = `{{ count "value" . }}`
	th.Templates.Choice.Details = `{{ count "details" . }}`
	m, tt := boundMultichoice(t, th, 4, []any{"a", "b", "c", "d"})
	for range 3 {
		mframe(t, m, tt)
	}
	m.handleKey(' ')
	var out bytes.Buffer
	m.out = &out
	assert.NoError(t, m.themedAnswer())
	assert.Equal(t, "+ Fruit · a\n", out.String())
	// every item projects once, and only drawn items render their details
	assert.Equal(t, map[string]int{
		"value:a": 1, "value:b": 1, "value:c": 1, "value:d": 1,
		"details:a": 1, "details:b": 1,
	}, calls)
}

func TestThemedMultichoiceLazyConfirmsAfterExhaustion(t *testing.T) {
	useTheme(t, asciiTheme(false))
	stream := &completionChecklistIO{ready: make(chan struct{}), marker: "x banana"}
	got, err := MultichoiceLazy(t.Context(), "Fruit", lazySeq("apple", "banana"),
		WithDefault("banana"), WithInput(stream), WithOutput(stream))
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)
}

func TestThemedMultichoiceLazyProducerErrorRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	failing := func(yield func(string, error) bool) {
		if yield("apple", nil) {
			yield("", boom)
		}
	}
	o, out, restored := themedMultichoiceOpt(t, asciiTheme(false))
	_, err := MultichoiceLazy(t.Context(), "Fruit", failing, o)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedMultichoiceHelperErrorRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Templates.Multichoice.Row = `{{ fails .Value }}`
	o, _, restored := themedMultichoiceOpt(t, th, "\r")
	_, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Templates.Multichoice.Row")
	assert.True(t, *restored)

	th = asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Templates.Multichoice.AnswerValue = `{{ fails "x" }}`
	o, out, restored := themedMultichoiceOpt(t, th, "\r")
	_, err = Multichoice(t.Context(), "Fruit", fruits, o)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedMultichoicePasteFiltersAndCancellationRestores(t *testing.T) {
	o, _, _ := themedMultichoiceOpt(t, asciiTheme(false), "\x1b[200~ban\x1b[201~", " ", "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)

	ctx, cancel := context.WithCancel(t.Context())
	o, out, restored := themedMultichoiceOpt(t, asciiTheme(false))
	acquired := make(chan struct{})
	signal := opT(func(m *multichoice) error {
		next := m.makeTermIO
		m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			defer close(acquired)
			return next(in, out)
		}
		return nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := Multichoice(ctx, "Fruit", fruits, o, signal)
		done <- err
	}()
	<-acquired
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("prompt stayed blocked after cancellation")
	}
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedMultichoiceRolesStyleMarks(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	th := asciiTheme(false)
	th.Light = true
	m, tt := boundMultichoice(t, th, 10, []any{"a", "b"})
	m.handleKey(' ')
	got := mframe(t, m, tt)
	// focus and checked marks take the accent, the unchecked mark is muted
	roles := m.look.roles
	assert.True(t, len(roles.accent) > 0 && len(roles.muted) > 0)
	assert.Contains(t, got, string(roles.accent)+">")
	assert.Contains(t, got, string(roles.accent)+"x")
	assert.Contains(t, got, string(roles.muted)+"o")
}

func TestThemedMultichoiceOffKeepsLegacyRows(t *testing.T) {
	keepTheme(t)
	var out bytes.Buffer
	_, err := Multichoice(t.Context(), "Fruit", fruits, WithInput(strings.NewReader(" \r")), WithOutput(&out))
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "→ [x] apple")
}

// frameStaysOnScreen replays out, the output of a prompt over a, b, c, d, e
// on a terminal of six rows that confirmed c, on a bounded screen: the first
// frame shows the label at the top, no redraw scrolls a row away, and cleanup
// leaves only the answer.
func frameStaysOnScreen(t *testing.T, out string) {
	t.Helper()
	first := newBoundedScreen(60, 6)
	first.write(out[:strings.Index(out, "\x1b[")])
	assert.Equal(t, "? Fruit", first.visible()[0])
	assert.Equal(t, 0, len(first.scrolled))

	all := newBoundedScreen(60, 6)
	all.write(out)
	assert.Equal(t, 0, len(all.scrolled))
	assert.Equal(t, []string{"+ Fruit · c"}, all.visible())
}

func TestThemedMultichoiceFullHeightFrameKeepsLabelOnScreen(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\x1b[B", "\x1b[B", " ", "\x1b[A", "\x1b[B", "\r")
	six := opT(func(m *multichoice) error {
		next := m.makeTermIO
		m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
			tt, err := next(in, out)
			tt.Height = 6
			return tt, err
		}
		return nil
	})
	got, err := Multichoice(t.Context(), "Fruit", []string{"a", "b", "c", "d", "e"}, o, six)
	assert.NoError(t, err)
	assert.Equal(t, []string{"c"}, got)
	frameStaysOnScreen(t, out.String())
}
