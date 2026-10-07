// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestMultichoiceRenderInitializes(t *testing.T) {
	m := newMultichoice()
	m.Items = []any{"one", "two"}
	io := newTestTermIO(20, 6)
	m.itemTemplate = template.Must(template.New("item").Parse("{{.}}"))
	m.moreItemsTemplate = template.Must(template.New("more").Parse("{{.More}}"))
	m.labelBuf.WriteString(m.Label + " ")

	frame := &viewport{
		ctx:      t.Context(),
		inner:    make(chan viewportWrite, 16),
		writeTos: make(chan *writeTo, 1),
		notify:   make(chan viewportChanged, 1),
		width:    io.Width,
		height:   io.Height,
	}

	assert.NoError(t, m.render(io, frame))
}

func TestMultichoiceRenderMoreItems(t *testing.T) {
	m := newMultichoice()
	m.Items = []any{"short", "veryverylong", "mid", "tail"}
	m.active = 0
	m.itemTemplate = template.Must(template.New("item").Parse("{{.}}"))
	m.moreItemsTemplate = template.Must(template.New("more").Parse("{{.More}}"))
	m.labelBuf.WriteString("longlabel ")
	io := newTestTermIO(5, 4)
	frame := &viewport{
		ctx:      t.Context(),
		inner:    make(chan viewportWrite, 16),
		writeTos: make(chan *writeTo, 1),
		notify:   make(chan viewportChanged, 16),
		width:    io.Width,
		height:   io.Height,
	}
	assert.NoError(t, m.render(io, frame))
}

func TestMultichoiceRunNoSpace(t *testing.T) {
	m := newMultichoice()
	m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
		return &termIO{
			in:      in,
			out:     out,
			Width:   10,
			Height:  2,
			Restore: func() error { return nil },
		}, nil
	}
	err := m.run()
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
	m := newMultichoice()
	m.Items = []any{"one", "two", "three"}
	m.selected = make([]bool, len(m.Items))
	m.itemTemplate = template.Must(template.New("item").Parse("{{.}}"))
	m.moreItemsTemplate = template.Must(template.New("more").Parse("{{.More}}"))
	m.labelBuf.WriteString(m.Label + " ")
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
	assert.NoError(t, m.run())
}

func TestMultichoiceRunIgnoresSpecialKeys(t *testing.T) {
	reader := &chunkReader{chunks: [][]byte{
		[]byte("\x1b[3~"),
		[]byte("\x1bx"),
		{0x1b, 0x1b, 0x5b, 0x42}, // Esc, then down
		{' '},
		{keyEnter},
	}}
	m := newMultichoice()
	m.Items = []any{"one", "two", "three"}
	m.selected = make([]bool, len(m.Items))
	m.itemTemplate = template.Must(template.New("item").Parse("{{.}}"))
	m.moreItemsTemplate = template.Must(template.New("more").Parse("{{.More}}"))
	m.labelBuf.WriteString(m.Label + " ")
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
	assert.NoError(t, m.run())
	assert.Equal(t, []bool{false, true, false}, m.selected)
}

func TestMultichoiceRunContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m := newMultichoice()
	m.Ctx = ctx
	m.Items = []any{"one"}
	m.selected = make([]bool, len(m.Items))
	m.itemTemplate = template.Must(template.New("item").Parse("{{.}}"))
	m.moreItemsTemplate = template.Must(template.New("more").Parse("{{.More}}"))
	m.labelBuf.WriteString(m.Label + " ")
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
	err := m.run()
	assert.Error(t, err)
}

func TestMultichoiceRunPasteIgnored(t *testing.T) {
	m := newMultichoice()
	m.Items = []any{"one"}
	m.selected = make([]bool, len(m.Items))
	m.itemTemplate = template.Must(template.New("item").Parse("{{.}}"))
	m.moreItemsTemplate = template.Must(template.New("more").Parse("{{.More}}"))
	m.labelBuf.WriteString(m.Label + " ")
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
	assert.NoError(t, m.run())
}

func TestMultichoiceRunReadError(t *testing.T) {
	m := newMultichoice()
	m.Items = []any{"one"}
	m.selected = make([]bool, len(m.Items))
	m.itemTemplate = template.Must(template.New("item").Parse("{{.}}"))
	m.moreItemsTemplate = template.Must(template.New("more").Parse("{{.More}}"))
	m.labelBuf.WriteString(m.Label + " ")
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
	err := m.run()
	assert.Error(t, err)
}

func TestMultichoiceClampDisplayKeepsActiveItemVisible(t *testing.T) {
	m := &multichoice{}
	m.relevant = []int{0, 1, 2, 3, 4, 5, 6}
	m.displayed = m.relevant[:5]
	m.active = 4

	m.clampDisplay(4) // two rows

	if got := m.relevant[m.offset+m.active]; got != 4 {
		t.Fatalf("active item changed: got %d, want 4", got)
	}
	if m.active < 0 || m.active >= len(m.displayed) {
		t.Fatalf("active %d outside displayed %d", m.active, len(m.displayed))
	}
}

func TestMultichoiceRunRerendersOnResize(t *testing.T) {
	keys, typing := io.Pipe()
	t.Cleanup(func() { _ = typing.Close() })
	resized := make(chan struct{})
	out := &syncBuffer{}
	var tio *termIO
	m := newMultichoice()
	m.Items = []any{"one", "two", "three", "four", "five", "six"}
	m.selected = make([]bool, len(m.Items))
	m.itemTemplate = template.Must(template.New("item").Parse("{{.}}\n"))
	m.moreItemsTemplate = template.Must(template.New("more").Parse("{{.More}} more"))
	m.labelBuf.WriteString(m.Label + " ")
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
	go func() { done <- m.run() }()
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
