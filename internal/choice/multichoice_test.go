// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

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
	"time"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

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

func waitBufferContains(t *testing.T, out *terst.SyncBuffer, part string) {
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
	m.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
		return &term.IO{In: in, Out: out, Width: 40, Height: 8, Restore: func() error { return nil }}, nil
	}
	return m
}

// preparedMultichoice builds item state for tests that bypass terminal interaction.
func preparedMultichoice(t *testing.T, items []any, o ...option.Opt) *multichoice {
	t.Helper()
	m := newMultichoice(t.Context())
	m.Items = items
	if err := m.prepare(o); err != nil {
		t.Fatal(err)
	}
	// binding projects the items and resolves the default, as run does
	if err := m.bindRun(t.Context(), terst.NewTermIO(60, 10)); err != nil {
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
	if m.focused() != -1 || m.count != 1 || !m.handleKey(term.KeyEnter) {
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
		tio := terst.NewTermIO(width, 6)
		var frame bytes.Buffer
		err := m.render(tio, &frame)
		if width <= 2 && errors.Is(err, errs.ErrNoSpace) {
			continue // the guide and the autowrap column leave no room for a row
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(frame.String(), "\n") {
			if !utf8.ValidString(line) || ansi.Text(line).Width() > width {
				t.Fatalf("width %d: %q", width, line)
			}
		}
	}
	m = preparedMultichoice(t, []any{"one"})
	var frame bytes.Buffer
	if err := m.render(terst.NewTermIO(0, 4), &frame); !errors.Is(err, errs.ErrNoSpace) {
		t.Fatal(err)
	}
	if err := m.render(terst.NewTermIO(20, 2), &frame); !errors.Is(err, errs.ErrNoSpace) {
		t.Fatal(err)
	}
}

func TestMultichoicePreparationAndCapabilities(t *testing.T) {
	m := preparedMultichoice(t, []any{"one", "two"}, option.WithDefault("one"), option.WithDefault("two"))
	if m.typed != "" || !reflect.DeepEqual(m.indices(), []int{1}) {
		t.Fatal(m.typed, m.indices())
	}
	m.handleKey(' ')
	m.filter("one")
	m.filter("")
	if m.count != 0 {
		t.Fatal("default was reapplied")
	}
	for _, one := range []option.Opt{WithTemplate(".Name")} {
		if err := one(newMultichoice(t.Context())); err != nil {
			t.Fatal(err)
		}
		if err := one(newDropdown(t.Context())); err != nil {
			t.Fatal(err)
		}
		if err := one(&struct{}{}); !errors.Is(err, errs.ErrWrongWidget) {
			t.Fatalf("input accepted choice option: %v", err)
		}
	}
	var out bytes.Buffer
	invalid := newMultichoice(t.Context())
	invalid.Items = []any{"one"}
	invalid.in = strings.NewReader("\r")
	invalid.out = &out
	acquired := false
	invalid.makeTermIO = func(io.Reader, io.Writer) (*term.IO, error) { acquired = true; return nil, errs.ErrNoTTY }
	for _, one := range []option.Opt{WithTemplate("{{"), WithTemplate(".Missing")} {
		// syntax fails preparation; templates run when the theme binds
		err := invalid.prepare([]option.Opt{one})
		if err == nil {
			err = invalid.bindRun(t.Context(), terst.NewTermIO(60, 10))
		}
		if err == nil {
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
		{"no space", io.Discard, "\r", 2, nil, errs.ErrNoSpace},
		{"primary and cleanup", choiceFailWriter{err: sentinel}, "\r", 8, restoreErr, sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := theme.New()
			th.Multichoice.Answer = `ANSWER {{ .Count }}`
			terst.UseTheme(t, th)
			m := newMultichoice(t.Context())
			m.Items = []any{"one"}
			m.in = strings.NewReader(tc.keys)
			m.out = tc.writer
			restores := 0
			m.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				return &term.IO{
					In:      in,
					Out:     out,
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
		terst.UseIO(t, strings.NewReader("\r"), io.Discard)
		if err := m.run(m.Ctx); err != nil {
			t.Fatal(err)
		}
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
	terst.UseIO(t, in, io.Discard)
	got, err := Multichoice(t.Context(), "Pick", []string{"one"})
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	if in.reads != 1 {
		t.Fatal("read started after Enter", in.reads)
	}
	terst.UseIO(t, in, io.Discard)
	got, err = Multichoice(t.Context(), "Next", []string{"two"})
	if !errors.Is(err, io.EOF) || got != nil {
		t.Fatal("consumed keys replayed", got, err)
	}
	term.InputStates.Lock()
	_, retained := term.InputStates.States[in]
	term.InputStates.Unlock()
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
	done, err := m.acceptEvent(&term.IO{}, term.KeyEvent{Key: term.KeyEnter, ReadOrder: m.readyOrder - 1})
	if done || err != nil {
		t.Fatal(done, err)
	}
	done, err = m.acceptEvent(&term.IO{}, term.KeyEvent{Key: term.KeyEnter, ReadOrder: m.readyOrder})
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
	out := &terst.SyncBuffer{}
	first.out = out
	done := make(chan error, 1)
	go func() { done <- first.run(first.Ctx) }()
	waitBufferContains(t, out, "○ one")
	terst.UseIO(t, strings.NewReader(" \r"), io.Discard)
	got, err := Multichoice(
		t.Context(),
		"Independent",
		[]string{"two"},
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

func TestMultichoiceStaticAndLazyLargeLists(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	items := make([]string, 10000)
	for i := range items {
		items[i] = fmt.Sprintf("item-%05d", i)
	}
	terst.UseIO(t, strings.NewReader("\r"), io.Discard)
	got, err := Multichoice(
		ctx,
		"Large",
		items,
		option.WithDefault("item-09999"),
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
			cio := term.NewUnstartedIO(ctx, 40, 8, 0)
			sentinel := errors.New("terminal failed")
			go cio.HandleViewports(ctx)
			go cio.ForwardTo(ctx, choiceFailWriter{err: sentinel, short: short})
			m := newMultichoice(t.Context())
			m.Items = []any{"one"}
			m.in = strings.NewReader("\r")
			m.out = cio
			var vp *term.Viewport
			m.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
				var err error
				vp, err = cio.PushViewport()
				if err != nil {
					return nil, err
				}
				return term.WithRestore(
					&term.IO{In: in, Out: out, Width: 40, Height: 8, Cio: cio, Vp: vp},
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
			cio.ChainMu.Lock()
			released := true
			for current := cio.Head; current != nil; current = current.Next {
				if current == vp {
					released = false
				}
			}
			cio.ChainMu.Unlock()
			if !released {
				t.Fatal("viewport not released")
			}
			cancel()
			<-cio.Done
		})
	}
}

// completionChecklistIO waits for the unique default to be checked after
// exhaustion, which a frame shows with marker, the checked glyph when empty.
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
		marker = "◉"
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
	p[0] = term.KeyEnter
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
	terst.UseIO(t, stream, stream)
	got, err := MultichoiceLazy(
		ctx,
		"Burst",
		seq,
		option.WithDefault("1999"),
		option.WithHide(),
	)
	if err != nil || !reflect.DeepEqual(got, []int{1999}) {
		t.Fatal(got, err)
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
			in := terst.NewKeys(t, "\t\r", "\r")
			old := &term.IO{In: in, SplitKeys: tc.split}
			read, accept := old.StartRead(t.Context())
			<-read.Done
			accept()
			if read.Event.ReadOrder == 0 {
				t.Fatal("source read has no arrival order")
			}
			if err := m.finishLoading(); err != nil {
				t.Fatal(err)
			}
			if !tc.split {
				if done, err := m.acceptEvent(old, read.Event); done || err != nil {
					t.Fatal(done, err)
				}
			}
			// A fresh termIO must preserve both pending and replayed arrival orders.
			next := &term.IO{In: in, SplitKeys: true, BracketedPaste: true}
			for range tc.buffered {
				buffered, consume := next.StartRead(t.Context())
				<-buffered.Done
				consume()
				if buffered.Event.ReadOrder != read.Event.ReadOrder {
					t.Fatal("buffered bytes lost their source-read order")
				}
				if done, err := m.acceptEvent(next, buffered.Event); done || err != nil {
					t.Fatalf("old Enter confirmed after loading: %v, %v", done, err)
				}
			}
			fresh, consume := next.StartRead(t.Context())
			<-fresh.Done
			consume()
			if fresh.Event.ReadOrder <= m.readyOrder {
				t.Fatal("fresh Enter retained an old arrival order")
			}
			if done, err := m.acceptEvent(next, fresh.Event); !done || err != nil {
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
	old := &term.IO{In: in, SplitKeys: true, BracketedPaste: true}
	read, accept := old.StartRead(t.Context())
	waitPromptRead(t, in)
	if _, err := typing.Write([]byte("\x1b[200~one")); err != nil {
		t.Fatal(err)
	}
	// The decoder has observed the opening frame and is waiting for its end.
	waitPromptRead(t, in)
	readyOrder := term.NextInputOrder()
	if _, err := typing.Write([]byte("\x1b[201~\r")); err != nil {
		t.Fatal(err)
	}
	<-read.Done
	accept()
	if !read.Event.Bracketed || read.Event.ReadOrder >= readyOrder {
		t.Fatal("paste lost the arrival order of its opening frame")
	}
	next := &term.IO{In: in, SplitKeys: true, BracketedPaste: true}
	tail, consume := next.StartRead(t.Context())
	<-tail.Done
	consume()
	if tail.Event.Key != term.KeyEnter || tail.Event.ReadOrder <= readyOrder {
		t.Fatal("Enter after the closing frame retained the paste's old arrival order")
	}
}

// acquireDefaultMultichoice inspects stream routing at terminal acquisition, after validation.
func acquireDefaultMultichoice(t *testing.T) *multichoice {
	t.Helper()
	m := newMultichoice(t.Context())
	m.Items = []any{"one"}
	m.makeTermIO = func(io.Reader, io.Writer) (*term.IO, error) { return nil, errs.ErrNoTTY }
	assert.ErrorIs(t, m.run(m.Ctx), errs.ErrNoTTY)
	return m
}

func TestMultichoiceDefaultStreams(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(strconv.FormatBool(tty), func(t *testing.T) {
			prevIn, prevOut := term.Default.Input(), term.Default.RawOutput()
			prevTTY, prevSize := term.WriterChecker, term.GetSize
			t.Cleanup(func() {
				term.WriterChecker, term.GetSize = prevTTY, prevSize
				term.Default.SetIO(prevIn, prevOut)
			})
			in := bytes.NewBufferString("in")
			var out io.Writer = &bytes.Buffer{}
			if tty {
				out = &terst.MockDescriptor{Writer: &bytes.Buffer{}, FD: 7}
			}
			term.WriterChecker = func(int) bool { return tty }
			term.GetSize = func(int) (int, int, error) { return 80, 24, nil }
			term.Default.SetIO(in, out)
			wantOut := out
			if tty {
				_ = term.Default.StderrWriter()
				_, wantOut = term.DefaultStreams(t.Context())
				cio, ok := wantOut.(*term.ChanIO)
				assert.True(t, ok)
				assert.NotNil(t, cio)
			}
			m := acquireDefaultMultichoice(t)
			assert.Equal(t, in, m.in)
			assert.Equal(t, wantOut, m.out)
		})
	}
}

func TestMultichoiceDefaults(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()
	items := []string{"a", "b", "c", "d"}

	got, err := Multichoice(ctx, "Pick", items, option.WithDefault("b"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"b"}, got)

	// source order, not default order
	got, err = Multichoice(ctx, "Pick", items, option.WithDefault([]string{"d", "b"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"b", "d"}, got)

	got, err = Multichoice(ctx, "Pick", items, option.WithDefault([]string{}))
	assert.NoError(t, err)
	assert.Equal(t, []string{}, got)
	assert.True(t, got != nil)

	got, err = Multichoice(ctx, "Pick", items, option.WithDefault([]string(nil)))
	assert.NoError(t, err)
	assert.True(t, got != nil && len(got) == 0)

	for _, bad := range []any{[]string{"x"}, []string{"a", "a"}, []int{1}, 5} {
		_, err = Multichoice(ctx, "Pick", items, option.WithDefault(bad))
		assert.ErrorIs(t, err, errs.ErrInvalidOption)
	}
	_, err = Multichoice(ctx, "Pick", []string{"a", "a"}, option.WithDefault([]string{"a"}))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	// a sole item is not consent
	_, err = Multichoice(ctx, "Pick", []string{"a"})
	assert.ErrorIs(t, err, errs.ErrNoTTY)
	_, err = Multichoice(ctx, "Pick", []string{"a"}, WithOneReturn())
	assert.ErrorIs(t, err, errs.ErrWrongWidget)

	// when T is a slice, the whole value is one item first
	groups := [][]string{{"a"}, {"b", "c"}}
	grp, err := Multichoice(ctx, "Pick", groups, option.WithDefault([]string{"b", "c"}))
	assert.NoError(t, err)
	assert.Equal(t, [][]string{{"b", "c"}}, grp)
	grp, err = Multichoice(ctx, "Pick", groups, option.WithDefault([][]string{{"b", "c"}, {"a"}}))
	assert.NoError(t, err)
	assert.Equal(t, groups, grp)

	structs := []server{{"alpha", 1}, {"beta", 2}}
	st, err := Multichoice(ctx, "Pick", structs, option.WithDefault(server{"beta", 2}))
	assert.NoError(t, err)
	assert.Equal(t, structs[1:], st)
}

func TestMultichoiceMapDefaults(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()
	items := map[string]int{"one": 1, "two": 2, "three": 3}

	keys, err := MultichoiceKVK(ctx, "Pick", items, option.WithDefault([]string{"two", "one"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, keys)

	values, err := MultichoiceKVV(ctx, "Pick", items, option.WithDefault([]int{3, 1}))
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 3}, values)

	value, err := MultichoiceKVV(ctx, "Pick", items, option.WithDefault(2))
	assert.NoError(t, err)
	assert.Equal(t, []int{2}, value)

	// the API decides the projection when K == V
	same := map[int]int{1: 2, 2: 1}
	k, err := MultichoiceKVK(ctx, "Pick", same, option.WithDefault(1))
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, k)
	v, err := MultichoiceKVV(ctx, "Pick", same, option.WithDefault(1))
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, v)

	_, err = MultichoiceKVV(ctx, "Pick", map[string]int{"a": 1, "b": 1}, option.WithDefault(1))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = MultichoiceKVK(ctx, "Pick", items, option.WithDefault([]int{1}))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
}

func TestTypedDefaultChecksInteractiveMultichoice(t *testing.T) {
	var out bytes.Buffer
	terst.UseIO(t, strings.NewReader("\r"), &out)
	got, err := Multichoice(t.Context(), "Pick", []string{"a", "b", "c"},
		option.WithDefault([]string{"c", "a"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, got)
	assert.Contains(t, out.String(), "◉ c")
}
