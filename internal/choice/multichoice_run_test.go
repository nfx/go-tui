// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/tcommon"
	"github.com/nfx/go-tui/internal/term"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/terst"
)

func TestMultichoiceBatchesQueuedArrivals(t *testing.T) {
	m := preparedMultichoice(t, []any{"initial"}, option.WithDefault("last"))
	m.loading = true
	arrivals := make(chan itPair, 2)
	arrivals <- itPair{item: "middle"}
	arrivals <- itPair{item: "last"}
	close(arrivals)

	// The first arrival and the full queue fit in one batch. The limit leaves
	// exhaustion for the next turn, without dropping an item or checking early.
	exhausted, err := m.addBatch(arrivals, itPair{item: "first"}, true)
	assert.NoError(t, err)
	assert.Equal(t, false, exhausted)
	assert.Equal(t, []any{"initial", "first", "middle", "last"}, m.Items)
	assert.Equal(t, true, m.loading)
	assert.Equal(t, 0, m.count)

	pair, more := <-arrivals
	exhausted, err = m.addBatch(arrivals, pair, more)
	assert.NoError(t, err)
	assert.Equal(t, true, exhausted)
	assert.Equal(t, false, m.loading)
	assert.Equal(t, []int{3}, m.indices())
	assert.True(t, m.readyOrder > 0)
}

func TestMultichoiceBatchStopsOnProducerError(t *testing.T) {
	m := preparedMultichoice(t, []any{"initial"})
	m.loading = true
	boom := errors.New("producer failed")
	arrivals := make(chan itPair, 2)
	arrivals <- itPair{err: boom}
	arrivals <- itPair{item: "unreachable"}

	exhausted, err := m.addBatch(arrivals, itPair{item: "first"}, true)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, false, exhausted)
	assert.Equal(t, []any{"initial", "first"}, m.Items)
	assert.Equal(t, true, m.loading)
}

func TestMultichoiceRunNoSpace(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"one"}
	m.out = io.Discard
	m.makeTermIO = tcommon.FixedTermIO(10, 2)
	err := m.run(m.Ctx)
	assert.ErrorIs(t, err, errs.ErrNoSpace)
}

func TestMultichoiceRunHandlesKeys(t *testing.T) {
	reader := terst.NewKeys(t, terst.KeyDown, " ", "z", "o", terst.KeyBackspace, terst.KeyUp, terst.KeyEnter)
	m := newMultichoice(t.Context())
	m.Items = []any{"one", "two", "three"}
	m.in = reader
	m.out = &bytes.Buffer{}
	m.makeTermIO = tcommon.FixedTermIO(40, 6)
	assert.NoError(t, m.run(m.Ctx))
}

func TestMultichoiceRunIgnoresSpecialKeys(t *testing.T) {
	reader := terst.NewKeys(t, "\x1b[3~", "\x1bx", terst.KeyEsc+terst.KeyDown, " ", terst.KeyEnter)
	m := newMultichoice(t.Context())
	m.Items = []any{"one", "two", "three"}
	m.in = reader
	m.out = &bytes.Buffer{}
	m.makeTermIO = tcommon.FixedTermIO(40, 6)
	assert.NoError(t, m.run(m.Ctx))
	assert.Equal(t, []bool{false, true, false}, m.selected)
}

func TestMultichoiceRunContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m := newMultichoice(ctx)
	m.Items = []any{"one"}
	m.in = terst.NewKeys(t, terst.KeyEnter)
	m.out = &bytes.Buffer{}
	m.makeTermIO = tcommon.FixedTermIO(40, 6)
	err := m.run(m.Ctx)
	assert.Error(t, err)
}

func TestMultichoiceRunCoalescedKeys(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"one"}
	m.in = terst.NewKeys(t, "ab", terst.KeyEnter)
	m.out = &bytes.Buffer{}
	m.makeTermIO = tcommon.FixedTermIO(40, 6)
	assert.NoError(t, m.run(m.Ctx))
}

func TestMultichoiceRunReadError(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"one"}
	m.in = bytes.NewBuffer(nil)
	m.out = &bytes.Buffer{}
	m.makeTermIO = tcommon.FixedTermIO(40, 6)
	err := m.run(m.Ctx)
	assert.Error(t, err)
}

func TestMultichoiceRunRerendersOnResize(t *testing.T) {
	keys, typing := io.Pipe()
	t.Cleanup(func() { _ = typing.Close() }) //nolint:errcheck // test cleanup
	resized := make(chan struct{})
	out := terst.NewOutput(t)
	var tio *term.IO
	m := newMultichoice(t.Context())
	m.Items = []any{"one", "two", "three", "four", "five", "six"}
	m.in = keys
	m.out = out
	m.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
		tio = &term.IO{
			In:       in,
			Out:      out,
			Width:    40,
			Height:   8,
			Restore:  func() error { return nil },
			OnResize: resized,
		}
		return tio, nil
	}
	done := make(chan error, 1)
	go func() { done <- m.run(m.Ctx) }()
	out.AwaitOutput("2 more")

	// shrink the terminal without pressing a key, to two rows and the count
	tio.Height = 5
	terst.Send(t, resized, struct{}{})
	out.AwaitOutput("4 more")

	_, err := typing.Write([]byte{term.KeyEnter})
	assert.NoError(t, err)
	err = terst.Recv(t, done)
	assert.NoError(t, err)
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
	err = terst.Recv(t, done)
	assert.NoError(t, err)
	assert.Equal(t, 1, m.active)
}

func TestMultichoiceRunCancelledWrappedReadKeepsNextKey(t *testing.T) {
	in := terst.NewKeys(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := newPipeMultichoice(ctx, in)
	finished := make(chan error, 1)
	go func() { finished <- m.run(m.Ctx) }()
	in.AwaitRead()
	cancel()
	err := terst.Recv(t, finished)
	assert.ErrorIs(t, err, context.Canceled)
	// the blocked read completes only now, after its prompt is gone
	in.Send("x", terst.KeyEnter)
	assert.Equal(t, 'x', in.NextKey())
}

func TestMultichoiceBracketedPasteSplitsAndHandoff(t *testing.T) {
	paste := "\x1b[200~two\r\n\t\x03\x04\x1b[201~"
	for split := 1; split < len(paste); split++ {
		t.Run(strconv.Itoa(split), func(t *testing.T) {
			keys := terst.UseKeys(t)
			keys.Send(paste[:split], paste[split:])
			keys.Type(" \r \r")
			first, err := Multichoice(
				t.Context(),
				"First",
				[]string{"one", "two"},
			)
			if err != nil || !reflect.DeepEqual(first, []string{"two"}) {
				t.Fatalf("%v, %v", first, err)
			}
			next, err := Multichoice(
				t.Context(),
				"Next",
				[]string{"next"},
			)
			if err != nil || !reflect.DeepEqual(next, []string{"next"}) {
				t.Fatalf("handoff: %v, %v", next, err)
			}
		})
	}
	// A legacy event produced by a cancelled old decoder must replay ordinary
	// keys, including the bytes after confirmation, through the shared decoder.
	terst.UseKeys(t).Type(" \r \r")
	in, _ := term.DefaultStreams(t.Context())
	old := &term.IO{In: in, Input: term.SharedInputState(in)}
	read, _ := old.StartRead(t.Context())
	<-read.Done
	first, err := Multichoice(
		t.Context(),
		"First",
		[]string{"one"},
	)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	second, err := Multichoice(
		t.Context(),
		"Second",
		[]string{"two"},
	)
	if err != nil || len(second) != 1 {
		t.Fatal(second, err)
	}
}

func TestMultichoiceCancelledPasteDiscardsPartialUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	in := terst.NewKeys(t)
	m := newPipeMultichoice(ctx, in)
	m.out = terst.NewOutput(t)
	done := make(chan error, 1)
	go func() { done <- m.run(m.Ctx) }()
	in.AwaitRead()
	in.Send("\x1b[200~one")
	// Wait until the opening frame is decoded and the payload read is blocked.
	in.AwaitRead()
	cancel()
	err := terst.Recv(t, done)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if m.typed != "" {
		t.Fatal("partial payload applied")
	}
	next := newPipeMultichoice(t.Context(), in)
	next.Items = []any{"next"}
	next.out = io.Discard
	go func() { done <- next.run(next.Ctx) }()
	in.Send("\x1b[201~ \r")
	err = terst.Recv(t, done)
	if err != nil {
		t.Fatal(err)
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
		{"empty", nil, "api", nil, nil, errs.ErrEmptyLazyResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMultichoice(t.Context())
			m.loading = true
			m.hint = tc.hint
			if err := m.prepare(nil); err != nil {
				t.Fatal(err)
			}
			if err := m.bindRun(t.Context(), tcommon.FixedTermIO(60, 10).IO()); err != nil {
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
			if m.handleKey(term.KeyEnter) {
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
	if err := m.bindRun(t.Context(), tcommon.FixedTermIO(60, 10).IO()); err != nil {
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

func TestMultichoiceLazyPublicLoading(t *testing.T) {
	arrivals := make(chan string)
	keys, out := terst.UseTerminal(t)
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
		got, err := MultichoiceLazy(
			t.Context(),
			"Lazy",
			seq,
			option.WithDefault("api"),
		)
		result <- got
		failure <- err
	}()
	out.AwaitOutput("Lazy")
	arrivals <- "api"
	out.AwaitOutput("○ api")
	keys.Type("\rx")
	out.AwaitOutput("No matches")
	keys.Type(terst.KeyBackspace)
	out.AwaitOutput("○ api")
	arrivals <- "worker"
	close(arrivals)
	out.AwaitOutput("◉ api")
	select {
	case err := <-failure:
		t.Fatalf("queued Enter confirmed after loading: %v", err)
	default:
	}
	keys.Type(terst.KeyEnter)
	err := terst.Recv(t, failure)
	if err != nil {
		t.Fatal(err)
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
		{"empty", func(func(string, error) bool) {}, errs.ErrEmptyLazyResult},
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
			terst.UseKeys(t)
			got, err := MultichoiceLazy(ctx, "Lazy", tc.seq)
			if got != nil || !errors.Is(err, tc.want) {
				t.Fatal(got, err)
			}
		})
	}
	got, err := MultichoiceLazy[string](t.Context(), "Nil", nil)
	if got != nil || !errors.Is(err, errs.ErrInvalidOption) {
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
	terst.Recv(t, stopped)
	if len(publication) > 16 {
		t.Fatal("unbounded queue")
	}
}

func TestMultichoicePasteModeLifetime(t *testing.T) {
	tcommon.Swap(t, &term.InputChecker, func(int) bool { return true })
	for _, keys := range []string{"\r", "\x03"} {
		var out strings.Builder
		in := &tcommon.MockDescriptor{Reader: strings.NewReader(keys), FD: 1}
		m := newMultichoice(t.Context())
		m.Items = []any{"one"}
		m.in = in
		m.out = &out
		restores := 0
		m.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			return &term.IO{
				In:      in,
				Out:     out,
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

func TestMultichoiceLazyDuplicateAndNilProjection(t *testing.T) {
	seq := func(yield func([]int, error) bool) {
		for _, item := range [][]int{{1}, {1}, {2}} {
			if !yield(item, nil) {
				return
			}
		}
	}
	got, err := answerWhenShown(t, "◉", " \x1b[B \x1b[B \r", func() ([][]int, error) {
		return MultichoiceLazy(t.Context(), "Equal", seq, option.WithDefault("[2]"), option.WithHide())
	})
	if err != nil || !reflect.DeepEqual(got, [][]int{{1}, {1}}) {
		t.Fatal(got, err)
	}
	nils := []any{nil, (*int)(nil)}
	anySeq := func(yield func(any, error) bool) {
		for _, item := range append(nils, "ready") {
			if !yield(item, nil) {
				return
			}
		}
	}
	values, err := answerWhenShown(t, "◉", " \x1b[B \x1b[B \r", func() ([]any, error) {
		return MultichoiceLazy(t.Context(), "Nil", anySeq, option.WithDefault("ready"), option.WithHide())
	})
	if err != nil || !reflect.DeepEqual(values, nils) {
		t.Fatal(values, err)
	}
}

func TestMultichoiceLegacyHandoffPreservesPasteFraming(t *testing.T) {
	for _, payload := range []string{" ", " \r", " \x03", " \x04", "\r\x03\x04 "} {
		t.Run(strconv.Quote(payload), func(t *testing.T) {
			keys, out := terst.UseTerminal(t)
			cancelPromptRead(t, keys, func(ctx context.Context) error {
				_, err := Dropdown(ctx, "Old", []string{"one"})
				return err
			})
			ctx, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			done := make(chan promptChoiceResult, 1)
			go func() {
				items, err := Multichoice(
					ctx,
					"Next",
					[]string{"one", "two"},
				)
				done <- promptChoiceResult{items, err}
			}()
			out.AwaitOutput("○ one")
			keys.Send("\x1b[200~" + payload + "\x1b[201~")
			// A new read proves the entire paste was accepted without confirming
			// or treating Ctrl-C/D as EOF; no timing delay is needed.
			keys.AwaitRead()
			select {
			case got := <-done:
				t.Fatalf("paste ended the prompt: %v, %v", got.items, got.err)
			default:
			}
			keys.Send(terst.KeyEnter)
			got := terst.Recv(t, done)
			if got.err != nil || got.items == nil || len(got.items) != 0 {
				t.Fatalf("pasted Space checked an item: %v, %v", got.items, got.err)
			}
		})
	}
}

func TestMultichoiceFreshPasteAfterCancelledRead(t *testing.T) {
	keys, out := terst.UseTerminal(t)
	cancelPromptRead(t, keys, func(ctx context.Context) error {
		_, err := Multichoice(ctx, "Old", []string{"old"})
		return err
	})
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	done := make(chan promptChoiceResult, 1)
	go func() {
		items, err := Multichoice(ctx, "Next", []string{"one", "two"})
		done <- promptChoiceResult{items, err}
	}()
	out.AwaitOutput("○ one")
	keys.Type("\x1b[200~two\x1b[201~ \r")
	got := terst.Recv(t, done)
	if got.err != nil || !reflect.DeepEqual(got.items, []string{"two"}) {
		t.Fatalf("fresh paste was discarded: %v, %v", got.items, got.err)
	}
}

func TestMultichoiceLazyDefaults(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()
	var started bool

	_, err := MultichoiceLazy(ctx, "Pick", seq(&started, nil, "a"))
	assert.ErrorIs(t, err, errs.ErrNoTTY)
	assert.True(t, !started)

	got, err := MultichoiceLazy(
		ctx,
		"Pick",
		seq(&started, nil, "a", "b", "c"),
		option.WithDefault([]string{"c", "a"}),
	)
	assert.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, got)

	_, err = MultichoiceLazy(ctx, "Pick", seq(&started, nil, "a", "a"), option.WithDefault([]string{"a"}))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	late := errors.New("late")
	_, err = MultichoiceLazy(ctx, "Pick", seq(&started, late, "a"), option.WithDefault("a"))
	assert.ErrorIs(t, err, late)

	_, err = MultichoiceLazy(ctx, "Pick", seq[string](&started, nil), option.WithDefault("a"))
	assert.ErrorIs(t, err, errs.ErrEmptyLazyResult)
}

func TestMultichoiceLazyAutomaticAnswerCancelsWhileProducerBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})
	defer close(release)
	seq := func(_ func(string, error) bool) {
		<-release
	}
	done := make(chan error, 1)
	terst.Quiet(t)
	go func() {
		_, err := MultichoiceLazy(ctx, "Pick", seq, option.WithDefault("a"))
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
