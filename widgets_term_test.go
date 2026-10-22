// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/option"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestChanIO_Forward(t *testing.T) {
	t.SkipNow()
	cio, stdout := chainIOforTest(t, 12, 3)

	// write 5 lines
	_, err := fmt.Fprint(cio, "a\nb\nc\nd\ne\n")
	assert.NoError(t, err)

	// render 3 lines due to the viewport height
	assert.Equal(t, "\rc           \n\rd           \n\re           ", <-stdout.C)

	// write one more line
	_, err = fmt.Fprint(cio, "f\n")
	assert.NoError(t, err)

	// and have the previous two lines still rendered
	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\rd           \n\re           \n\rf           ",
		<-stdout.C)

	ticks := make(chan time.Time)
	defer close(ticks)
	tick := func() {
		go func() {
			select {
			case <-cio.ctx.Done():
			case ticks <- time.Now():
			}
		}()
	}

	s, err := NewSpinners(cio.ctx,
		WithOutput(cio),
		option.OpT(func(s *Spinners) error { //nolint:unparam // ..
			s.ticks = ticks
			return nil
		}),
	)
	assert.NoError(t, err)

	s.MustAddBackground().Update("s: A")
	tick()

	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\r\r... s: A   \n\r\r           \n\rf           ",
		<-stdout.C)

	// write one more line
	_, err = fmt.Fprint(cio, "g\n")
	assert.NoError(t, err)

	assert.Equal(t,
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r\r\r... s: A   \n\r\r           \n\rg           ",
		<-stdout.C)
}

func TestInput_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	value, err := Input(context.Background(), "Name",
		WithInput(strings.NewReader("alice\r")), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "alice", value)
	assert.Contains(t, out.String(), "Name")
}

func TestSharedInputState_CancelledClosedReaderReleased(t *testing.T) {
	pr, pw, err := os.Pipe()
	assert.NoError(t, err)
	defer pr.Close() //nolint:errcheck // test cleanup
	defer pw.Close() //nolint:errcheck // test cleanup
	in := &notifyingReader{File: pr, started: make(chan struct{})}
	tio := &termIO{in: in}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tio.readEvents(ctx, nil)
	<-in.started
	cancel()
	tio.awaitReader()
	assert.NoError(t, pr.Close())
	<-tio.input.read.done
	assert.Error(t, tio.input.read.event.err)
	inputStates.Lock()
	_, retained := inputStates.states[in]
	inputStates.Unlock()
	if retained {
		t.Fatal("closed input retained after its cancelled read finished")
	}
}

func TestInput_ScriptedInputDefaultOutput(t *testing.T) {
	value, err := Input(context.Background(), "Name", WithInput(strings.NewReader("bob\r")), WithOutput(io.Discard))
	assert.NoError(t, err)
	assert.Equal(t, "bob", value)
}

func TestDropdown_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	// Down, Enter
	value, err := Dropdown(context.Background(), "Pick", []string{"a", "b", "c"},
		WithInput(strings.NewReader("\x1b[B\r")), WithOutput(&out))
	assert.NoError(t, err)
	assert.Equal(t, "b", value)
}

func TestConfirm_ScriptedReaderAndWriter(t *testing.T) {
	var out bytes.Buffer
	ok := Confirm(context.Background(), "Proceed",
		WithInput(strings.NewReader("\r")), WithOutput(&out))
	assert.True(t, ok)
}

func TestDropdownPressKeyReadsSharedPendingEnter(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "same terminal"
		if fresh {
			name = "fresh terminal"
		}
		t.Run(name, func(t *testing.T) {
			in := &mockDescriptor{Reader: bytes.NewBufferString("\x1b[A\r"), fd: 0}
			first := &termIO{in: in, out: &bytes.Buffer{}}
			ev := <-first.readEvents(t.Context(), nil)
			first.awaitReader()
			assert.NoError(t, ev.err)
			assert.Equal(t, '↑', ev.key)
			second := first
			if fresh {
				second = &termIO{in: in, out: &bytes.Buffer{}}
			}
			d := newDropdown(t.Context())
			d.Items = []any{"one", "two"}
			d.relevant = []int{0, 1}
			i, err := d.pressKey(d.Ctx, second, &bytes.Buffer{}, 1, 2)
			assert.NoError(t, err)
			assert.Equal(t, 0, i)
			ev = <-second.readEvents(t.Context(), nil)
			second.awaitReader()
			assert.ErrorIs(t, ev.err, io.EOF)
		})
	}
}

func TestRoutingWrapperInputBufferOutput(t *testing.T) {
	strictOutput(t)
	var out bytes.Buffer
	got, err := Input(t.Context(), "Name",
		WithInput(strings.NewReader("typed\r")), WithOutput(&out), WithDefault("dflt"))
	assert.NoError(t, err)
	assert.Equal(t, "dflt", got)
	assert.Equal(t, "", out.String())

	_, err = Input(t.Context(), "Name", WithInput(strings.NewReader("typed\r")), WithOutput(&out))
	assert.ErrorIs(t, err, ErrNoTTY)

	f, ferr := os.CreateTemp(t.TempDir(), "out")
	assert.NoError(t, ferr)
	defer f.Close() //nolint:errcheck
	_, err = Dropdown(t.Context(), "Pick", []string{"a"}, WithOutput(f), WithInput(f))
	assert.ErrorIs(t, err, ErrNoTTY)
}

func TestInteractiveStillWorksOnManagedOutput(t *testing.T) {
	// production classification: a managed terminal with scripted input
	strictOutput(t)
	cio := startChanIO(t.Context(), 80, 24)
	got, err := Input(t.Context(), "Name", WithInput(strings.NewReader("bob\r")), WithOutput(cio))
	if errors.Is(err, ErrNoTTY) {
		t.Fatal("managed terminal must stay interactive")
	}
	_ = got
}
