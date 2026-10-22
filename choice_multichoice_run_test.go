// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

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

	"github.com/nfx/go-tui/internal/assert"
)

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

	// shrink the terminal without pressing a key, to two rows and the count
	tio.Height = 5
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
	got, err := next.run(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, "x", got)
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
			if err := m.bindRun(t.Context(), newTestTermIO(60, 10)); err != nil {
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
	if err := m.bindRun(t.Context(), newTestTermIO(60, 10)); err != nil {
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
	waitBufferContains(t, out, "○ api")
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
	waitBufferContains(t, out, "○ api")
	arrivals <- "worker"
	close(arrivals)
	waitBufferContains(t, out, "◉ api")
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
			waitBufferContains(t, out, "○ one")
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
	waitBufferContains(t, out, "○ one")
	if _, err := typing.Write([]byte("\x1b[200~two\x1b[201~ \r")); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || !reflect.DeepEqual(got.items, []string{"two"}) {
		t.Fatalf("fresh paste was discarded: %v, %v", got.items, got.err)
	}
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
