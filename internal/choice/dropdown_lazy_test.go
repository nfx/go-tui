// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"os"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/internal/trie"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestDropdownLazyClearsOnlyRenderedArea(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, d.parseTemplates())
	d.trie = trie.NewTrie()

	var (
		frame  bytes.Buffer
		output bytes.Buffer
	)
	tio := &term.IO{
		In:      bytes.NewBuffer(nil),
		Out:     &output,
		Width:   20,
		Height:  5,
		Restore: func() error { return nil },
	}
	space := 2
	err := d.loadItem(tio, &frame, itPair{item: "one"}, true, space)
	assert.NoError(t, err)

	var expected bytes.Buffer
	assert.NoError(t, tio.Clear(space, &expected))
	assert.Equal(t, expected.String(), output.String())
}

func TestDropdownHandleLazyItemEmpty(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, d.parseTemplates())
	tio := terst.NewTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{}, false)
	assert.ErrorIs(t, err, errs.ErrEmptyLazyResult)
}

func TestDropdownHandleLazyItemError(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, d.parseTemplates())
	tio := terst.NewTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{err: io.EOF}, true)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownHandleLazyItemAdds(t *testing.T) {
	d := newDropdown(t.Context())
	assert.NoError(t, d.parseTemplates())
	tio := terst.NewTermIO(20, 6)
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
	tio := terst.NewTermIO(20, 6)
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

func TestDropdownHandleLazyKeyEOF(t *testing.T) {
	d := newDropdown(t.Context())
	tio := terst.NewTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyKey(tio, frame, 1, 1, term.KeyEvent{}, false)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownHandleLazyKeyPasteIgnored(t *testing.T) {
	d := newDropdown(t.Context())
	tio := terst.NewTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyKey(tio, frame, 1, 1, term.KeyEvent{Err: &term.PasteTextError{Buf: []byte("a")}}, true)
	assert.NoError(t, err)
}

func TestDropdownHandleLazyKeyEnter(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"one"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	tio := terst.NewTermIO(20, 6)
	frame := &bytes.Buffer{}
	i, _, err := d.handleLazyKey(tio, frame, 1, 1, term.KeyEvent{Key: term.KeyEnter}, true)
	assert.NoError(t, err)
	assert.Equal(t, 0, i)
}

func TestDropdownRenderLazyFrameClears(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	bindDropdown(t, d)
	tio := terst.NewTermIO(20, 6)
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
	tio := &term.IO{
		In:      bytes.NewBuffer(nil),
		Out:     terst.FailingWriter{Err: io.EOF},
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

func TestDropdownHandleLazyKeyReadError(t *testing.T) {
	d := newDropdown(t.Context())
	tio := terst.NewTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyKey(tio, frame, 1, 1, term.KeyEvent{Err: io.EOF}, true)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDropdownNextLazyActionItem(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"seed"}
	assert.NoError(t, d.parseTemplates())
	tio := terst.NewTermIO(20, 6)
	_, err := d.renderInit(tio)
	assert.NoError(t, err)
	frame := bytes.NewBuffer(nil)
	d.itItems = make(chan itPair, 1)
	d.itItems <- itPair{item: "next"}
	res, err := d.nextLazyAction(tio, frame, 1, 1, make(chan term.KeyEvent))
	assert.NoError(t, err)
	assert.True(t, res.needsRender)
}

func TestDropdownNextLazyActionKey(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"seed"}
	d.relevant = []int{0}
	d.displayed = []int{0}
	assert.NoError(t, d.parseTemplates())
	tio := terst.NewTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	keys := make(chan term.KeyEvent, 1)
	keys <- term.KeyEvent{Key: term.KeyEnter}
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
	tio := terst.NewTermIO(20, 6)
	frame := bytes.NewBuffer(nil)
	_, err := d.nextLazyAction(tio, frame, 1, 1, make(chan term.KeyEvent))
	assert.Error(t, err)
}

func TestDropdownHandleLazyItemDoneWithItems(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"item"}
	assert.NoError(t, d.parseTemplates())
	tio := terst.NewTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{}, false)
	assert.NoError(t, err)
	assert.True(t, d.iterDone)
}

func TestDropdownHandleLazyItemAddError(t *testing.T) {
	d := newDropdown(t.Context())
	d.mainTemplate = template.Must(template.New("content").Parse("{{call .}}"))
	d.trie = trie.NewTrie()
	tio := terst.NewTermIO(20, 6)
	frame := &bytes.Buffer{}
	_, _, err := d.handleLazyItem(tio, frame, 1, itPair{item: "x"}, true)
	assert.Error(t, err)
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
	in, out, opts := testIOforDropdown(t, 20, 6)
	itemsLoaded, ready := notifyItemsLoaded(3)
	opts = option.WithOptions(opts, itemsLoaded)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy(t.Context(), "Select", lazySeq("red", "green", "blue"), opts)
		resCh <- struct {
			value string
			err   error
		}{value, err}
	}()
	waitForItems(t, ready)
	in.Send(terst.KeyEnter)
	final := out.AwaitOutput("+ Select · red")
	res := <-resCh
	assert.NoError(t, res.err)
	assert.Equal(t, "red", res.value)
	assert.Contains(t, final, "+ Select · red")
}

func TestDropdownLazyDoesNotLeaveBlockedReaderAfterConfirm(t *testing.T) {
	in := terst.NewKeys(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	itemsLoaded, ready := notifyItemsLoaded(2)
	terst.UseIO(t, in, io.Discard)
	opts := option.WithOptions(
		option.OpT(func(d *dropdown) error {
			d.makeTermIO = terst.FixedTermIO(20, 6)
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
	in.Send(terst.KeyEnter)
	res := <-resCh
	assert.NoError(t, res.err)
	assert.Equal(t, "red", res.value)
	assertNoWaitersFor(t, in, 200*time.Millisecond)
}

func TestDropdownLazyDoesNotStartProducerWhenContextAlreadyCanceled(t *testing.T) {
	in := terst.NewKeys(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var started atomic.Bool
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
	})
	terst.UseIO(t, in, io.Discard)
	opts := option.WithOptions(
		option.OpT(func(d *dropdown) error {
			d.makeTermIO = terst.FixedTermIO(20, 6)
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
	terst.UseIO(t, in, io.Discard)
	opts := option.WithOptions(
		option.OpT(func(d *dropdown) error {
			d.makeTermIO = terst.FixedTermIO(20, 6)
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
	_, err = writer.Write([]byte{term.KeyCtrlC})
	assert.NoError(t, err)
	err = terst.Recv(t, resCh)
	assert.ErrorIs(t, err, io.EOF)
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
	terst.UseIO(t, in, io.Discard)
	opts := option.WithOptions(
		option.OpT(func(d *dropdown) error {
			d.makeTermIO = terst.FixedTermIO(20, 6)
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
	_, err = writer.Write([]byte{term.KeyEnter})
	assert.NoError(t, err)
	res := terst.Recv(t, resCh)
	assert.NoError(t, res.err)
	assert.Equal(t, "b", res.value)
}

func TestDropdownLazyEmptySequence(t *testing.T) {
	_, _, opts := testIOforDropdown(t, 20, 6)
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy(t.Context(), "Empty", func(yield func(string, error) bool) {}, opts)
		resCh <- struct {
			value string
			err   error
		}{value, err}
	}()
	res := <-resCh
	assert.ErrorIs(t, res.err, errs.ErrEmptyLazyResult)
	assert.Equal(t, "", res.value)
}

func TestDropdownLazyOneReturnSingleItem(t *testing.T) {
	_, _, opts := testIOforDropdown(t, 20, 6, WithOneReturn())
	resCh := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := DropdownLazy(t.Context(), "Single", lazySeq("only"), opts)
		resCh <- struct {
			value string
			err   error
		}{value, err}
	}()
	res := terst.Recv(t, resCh)
	assert.NoError(t, res.err)
	assert.Equal(t, "only", res.value)
}

func TestDropdownLazyAppendAfterFilter(t *testing.T) {
	tio := terst.NewTermIO(20, 6)
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
	assert.NoError(t, option.WithDefault("cat")(d))
	bindDropdown(t, d)
	tio := terst.NewTermIO(20, 6)
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
	assert.NoError(t, option.WithDefault("cat")(d))
	bindDropdown(t, d)
	tio := terst.NewTermIO(20, 10)
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

func TestDropdownLazyStopsProducerOnEarlyError(t *testing.T) {
	var started atomic.Int32
	seq := func(yield func(int, error) bool) {
		started.Add(1)
		for i := 0; yield(i, nil); i++ {
		}
	}
	terst.UseTerminalInput(t, nil)
	_, err := DropdownLazy(t.Context(), "pick", seq,
		fixedTermOpt(), WithTemplate("{{"))
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
	terst.Recv(t, done)
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
		w.Write([]byte{term.KeyEnter}) //nolint:errcheck // test helper
	}()
	terst.UseIO(t, in, io.Discard)
	_, err = DropdownLazy(t.Context(), "pick", seq,
		fixedTermOpt())
	assert.NoError(t, err)
	terst.Recv(t, done)
}

func TestDropdownLazyDefaults(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()
	var started bool

	_, err := DropdownLazy(ctx, "Pick", seq(&started, nil, "a", "b"))
	assert.ErrorIs(t, err, errs.ErrNoTTY)
	assert.True(t, !started)

	got, err := DropdownLazy(ctx, "Pick", seq(&started, nil, "alpha", "beta"), option.WithDefault("beta"))
	assert.NoError(t, err)
	assert.Equal(t, "beta", got)

	// a late second match is ambiguous
	_, err = DropdownLazy(ctx, "Pick", seq(&started, nil, "a", "b", "a"), option.WithDefault("a"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	late := errors.New("late")
	_, err = DropdownLazy(ctx, "Pick", seq(&started, late, "a"), option.WithDefault("a"))
	assert.ErrorIs(t, err, late)

	_, err = DropdownLazy(ctx, "Pick", seq[string](&started, nil), option.WithDefault("a"))
	assert.ErrorIs(t, err, errs.ErrEmptyLazyResult)

	got, err = DropdownLazy(ctx, "Pick", seq(&started, nil, "only"), WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, "only", got)
	// a singleton at first is not final
	_, err = DropdownLazy(ctx, "Pick", seq(&started, nil, "a", "b"), WithOneReturn())
	assert.ErrorIs(t, err, errs.ErrNoTTY)

	typed, err := DropdownLazy(
		ctx,
		"Pick",
		seq(&started, nil, server{"a", 1}, server{"b", 2}),
		option.WithDefault(server{"b", 2}),
	)
	assert.NoError(t, err)
	assert.Equal(t, server{"b", 2}, typed)

	cctx, cancel := context.WithCancel(ctx)
	_, err = DropdownLazy(cctx, "Pick", func(yield func(string, error) bool) {
		cancel()
		yield("a", nil)
	}, option.WithDefault("a"))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestDropdownLazySeedValidatedAfterExhaustion(t *testing.T) {
	for name, items := range map[string][]any{"missing": {1, 2}, "duplicate": {2, 2}} {
		t.Run(name, func(t *testing.T) {
			d := newDropdown(t.Context())
			d.target = choiceTarget{}
			d.def = option.DefaultValue{Set: true, Raw: 3}
			if name == "duplicate" {
				d.def.Raw = 2
			}
			d.seedLazy = true
			d.Items = items
			err := d.validateLazySeed()
			assert.ErrorIs(t, err, errs.ErrInvalidOption)
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
	terst.Quiet(t)
	go func() {
		_, err := DropdownLazy(ctx, "Pick", seq, option.WithDefault("a"))
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
