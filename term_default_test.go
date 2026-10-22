// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

func TestNewIOReturnsOrErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err := NewIO(ctx)
	if err != nil {
		return
	}
}

func TestNewIOWithTTY(t *testing.T) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		t.Skip("tty not available")
	}
	origStderr := os.Stderr
	os.Stderr = tty
	t.Cleanup(func() {
		os.Stderr = origStderr
		assert.NoError(t, tty.Close())
	})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio, err := NewIO(ctx)
	if err != nil {
		t.Fatalf("NewIO failed: %v", err)
	}
	if cio == nil {
		t.Fatalf("expected io")
	}
}

func TestNewIOWithStubSize(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	t.Cleanup(func() { termGetSize = orig })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cio, err := NewIO(ctx)
	if err != nil {
		t.Fatalf("NewIO failed: %v", err)
	}
	if cio.width != 80 || cio.height != 24 {
		t.Fatalf("unexpected size %dx%d", cio.width, cio.height)
	}
}

func TestNewIOWithStubError(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 0, 0, errors.New("boom") }
	t.Cleanup(func() { termGetSize = orig })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err := NewIO(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrNoTTY) {
		t.Fatalf("expected ErrNoTTY, got %v", err)
	}
}

func TestSpinnersCloseNil(t *testing.T) {
	var s *Spinners
	s.Close()
}

func TestSetDefaultIO(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	in := bytes.NewBufferString("in")
	out := &bytes.Buffer{}
	SetDefaultIO(in, out)

	d := newDropdown(t.Context())
	assert.Equal(t, in, d.in)
	assert.Equal(t, out, d.out)

	input := newInput(t.Context(), "label")
	assert.Equal(t, in, input.in)
	assert.Equal(t, out, input.out)

	s := newSpinners(t.Context())
	assert.Equal(t, in, s.in)
	assert.Equal(t, out, s.out)
	s.ticker.Stop()

	p := newProgressbar(t.Context())
	assert.Equal(t, in, p.in)
	assert.Equal(t, out, p.out)
	p.ticker.Stop()
}

func TestWidgetsUseSharedDefaultTerminalOnTTY(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	prevTTY := terminalWriterChecker
	prevSize := termGetSize
	t.Cleanup(func() {
		terminalWriterChecker = prevTTY
		termGetSize = prevSize
		SetDefaultIO(prevIn, prevOut)
	})
	in := bytes.NewBufferString("in")
	out := &mockDescriptor{Writer: &bytes.Buffer{}, fd: 7}
	terminalWriterChecker = func(int) bool { return true }
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }
	SetDefaultIO(in, out)
	_ = Stderr()

	_, defaultOut := defaultStreams(t.Context())
	cio, ok := defaultOut.(*chanIO)
	assert.True(t, ok)
	assert.NotNil(t, cio)

	d := newDropdown(t.Context())
	assert.Equal(t, in, d.in)
	assert.Equal(t, cio, d.out)

	input := newInput(t.Context(), "label")
	assert.Equal(t, in, input.in)
	assert.Equal(t, cio, input.out)

	s := newSpinners(t.Context())
	assert.Equal(t, in, s.in)
	assert.Equal(t, cio, s.out)
	s.ticker.Stop()

	p := newProgressbar(t.Context())
	assert.Equal(t, in, p.in)
	assert.Equal(t, cio, p.out)
	p.ticker.Stop()
}

func TestConfirmDefaultTerminalHandlesArrowKeys(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 40, 6)
	reader := &blockingByteReader{ch: make(chan []byte, 2)}
	t.Cleanup(reader.Close)
	input := &mockDescriptor{
		Reader: reader,
		fd:     0,
	}
	SetDefaultIO(input, cio)

	result := make(chan bool, 1)
	go func() {
		result <- Confirm(t.Context(), "Are you sure?")
	}()

	initial := waitOutputContains(t, stdout.C, "Are you sure?", "Yes", "No")
	reader.SendBytes([]byte{0x1b, 0x5b, 0x42})
	updated := waitOutputContains(t, stdout.C, "Are you sure?", "Yes", "No")
	assert.True(t, initial != updated)
	reader.SendByte(keyEnter)
	assert.True(t, !<-result)
}

func TestInputDefaultTerminalHandlesTypedChars(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 40, 6)
	input := &mockDescriptor{
		Reader: &chunkReader{chunks: [][]byte{
			{'h'},
			{'i'},
			{keyEnter},
		}},
		fd: 0,
	}
	SetDefaultIO(input, cio)

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := Input(t.Context(), "Label")
		if err != nil {
			errs <- err
			return
		}
		result <- value
	}()

	// chunkReader delivers all keys at once, so intermediate frames may be
	// coalesced; only the final result is deterministic.
	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "hi", value)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Input to return")
	}
	_ = stdout
}

func TestInputLeftArrowMovesCursorWithSplitEscape(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 60, 8)
	reader := &blockingByteReader{ch: make(chan []byte, 8)}
	t.Cleanup(reader.Close)
	input := &mockDescriptor{Reader: reader, fd: 0}
	SetDefaultIO(input, cio)

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := Input(t.Context(), "PROMPT")
		if err != nil {
			errs <- err
			return
		}
		result <- value
	}()

	waitOutputContains(t, stdout.C, "PROMPT")
	reader.SendByte('a')
	reader.SendByte('b')
	reader.SendByte('c')
	waitOutputContains(t, stdout.C, "PROMPT", "abc")
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	reader.SendByte('X')
	reader.SendByte(keyEnter)

	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "abXc", value)
	}
}

func TestInputDefaultTerminalPreservesCursorAcrossLogs(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() {
		SetDefaultIO(prevIn, prevOut)
	})
	cio, stdout := chainIOforTest(t, 60, 8)
	reader := &blockingByteReader{ch: make(chan []byte, 32)}
	t.Cleanup(reader.Close)
	input := &mockDescriptor{Reader: reader, fd: 0}
	SetDefaultIO(input, cio)

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := Input(t.Context(), "PROMPT", WithDefault("apple"))
		if err != nil {
			errs <- err
			return
		}
		result <- value
	}()

	waitOutputContains(t, stdout.C, "PROMPT", "apple")
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	_, err := Stderr().Write([]byte("INF background\n"))
	assert.NoError(t, err)
	waitOutputContains(t, stdout.C, "INF background", "PROMPT", "apple")
	reader.SendByte(0x1b)
	reader.SendByte(0x5b)
	reader.SendByte(0x44)
	reader.SendByte('q')
	reader.SendByte('q')
	reader.SendByte('q')
	reader.SendByte(keyEnter)

	select {
	case err := <-errs:
		assert.NoError(t, err)
	case value := <-result:
		assert.Equal(t, "apqqqple", value)
	}
}

func TestTioCloseFlushesOutput(t *testing.T) {
	prevTTY := terminalWriterChecker
	prevSize := termGetSize
	t.Cleanup(func() {
		terminalWriterChecker = prevTTY
		termGetSize = prevSize
	})

	var out bytes.Buffer
	outW := &mockDescriptor{Writer: &out, fd: 42}
	terminalWriterChecker = func(int) bool { return true }
	termGetSize = func(int) (int, int, error) { return 80, 24, nil }

	tioInst := &tio{reader: bytes.NewBufferString(""), writer: outW}
	w := tioInst.stderrWriter()

	_, err := w.Write([]byte("pending output\n"))
	assert.NoError(t, err)

	assert.NoError(t, tioInst.Close())
	assert.True(t, strings.Contains(out.String(), "pending output"))
}

func waitOutputContains(t *testing.T, out <-chan string, parts ...string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-out:
			matched := true
			for _, part := range parts {
				if !strings.Contains(got, part) {
					matched = false
					break
				}
			}
			if matched {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for output containing %q", parts)
		}
	}
}

func TestDefaultStreamsArePairedUnderSetDefaultIO(t *testing.T) {
	prevIn := defaultIO.input()
	prevOut := defaultIO.rawOutput()
	t.Cleanup(func() { SetDefaultIO(prevIn, prevOut) })
	in1, out1 := bytes.NewBufferString("1"), &bytes.Buffer{}
	in2, out2 := bytes.NewBufferString("2"), &bytes.Buffer{}
	SetDefaultIO(in1, out1)

	stop := make(chan struct{})
	swapped := make(chan struct{})
	go func() {
		defer close(swapped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			SetDefaultIO(in2, out2)
			SetDefaultIO(in1, out1)
		}
	}()
	for range 1000 {
		in, out := defaultStreams(t.Context())
		paired := (in == io.Reader(in1) && out == io.Writer(out1)) ||
			(in == io.Reader(in2) && out == io.Writer(out2))
		if !paired {
			t.Fatalf("mismatched default streams: %p %p", in, out)
		}
	}
	close(stop)
	<-swapped
}

var regexpSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// withEnv installs an output environment fixture built from KEY=VALUE pairs.
func withEnv(t *testing.T, pairs ...string) {
	t.Helper()
	vars := map[string]string{}
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		vars[k] = v
	}
	prev := defaultIO.env
	defaultIO.env = readOutputEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
	t.Cleanup(func() { defaultIO.env = prev })
}

func ttyWriter(t *testing.T) *mockDescriptor {
	t.Helper()
	prev := terminalWriterChecker
	terminalWriterChecker = func(int) bool { return true }
	prevVT := consoleVT
	consoleVT = func(io.Writer) bool { return true }
	t.Cleanup(func() {
		terminalWriterChecker = prev
		consoleVT = prevVT
	})
	return &mockDescriptor{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}, fd: 7}
}

func TestProfileFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		tty  bool
		want colorProfile
	}{
		{"tty truecolor", []string{"COLORTERM=truecolor"}, true, profileTrueColor},
		{"tty 24bit", []string{"COLORTERM=24bit"}, true, profileTrueColor},
		{"tty 256", []string{"TERM=xterm-256color"}, true, profileANSI256},
		{"tty bare", nil, true, profileANSI16},
		{"tty windows terminal", []string{"WT_SESSION=x"}, true, profileTrueColor},
		{"pipe force 3", []string{"FORCE_COLOR=3"}, false, profileTrueColor},
		{"pipe force 2", []string{"FORCE_COLOR=2"}, false, profileANSI256},
		{"pipe force 1", []string{"FORCE_COLOR=1"}, false, profileANSI16},
		{"pipe force empty", []string{"FORCE_COLOR="}, false, profileANSI16},
		{"pipe force garbage", []string{"FORCE_COLOR=banana"}, false, profileANSI16},
		{"tty force 0", []string{"FORCE_COLOR=0"}, true, profilePlain},
		{"tty force false", []string{"FORCE_COLOR=false"}, true, profilePlain},
		{"unknown non-tty", nil, false, profilePlain},
		{"clicolor force", []string{"CLICOLOR_FORCE=1"}, false, profileANSI16},
		{"clicolor force 0", []string{"CLICOLOR_FORCE=0"}, false, profilePlain},
		{"clicolor force depth", []string{"CLICOLOR_FORCE=1", "COLORTERM=truecolor"}, false, profileTrueColor},
		{"no color tty", []string{"NO_COLOR=1"}, true, profileAttrs},
		{"no color pipe", []string{"NO_COLOR=1"}, false, profilePlain},
		{"force beats no color", []string{"NO_COLOR=1", "FORCE_COLOR=3"}, true, profileTrueColor},
		{"dumb", []string{"TERM=dumb"}, true, profilePlain},
		{"ci tty", []string{"CI=true", "COLORTERM=truecolor"}, true, profileAttrs},
		{"ci pipe", []string{"CI=true"}, false, profilePlain},
		{"ci 1", []string{"CI=1"}, true, profileAttrs},
		{"ci false", []string{"CI=false"}, true, profileANSI16},
		{"ci 0", []string{"CI=0"}, true, profileANSI16},
		{"ci clicolor force", []string{"CI=true", "CLICOLOR_FORCE=1"}, false, profileANSI16},
		{"ci force color", []string{"CI=true", "FORCE_COLOR=3"}, false, profileTrueColor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withEnv(t, tc.env...)
			var w io.Writer = &bytes.Buffer{}
			if tc.tty {
				w = ttyWriter(t)
			}
			assert.Equal(t, tc.want, defaultIO.profileFor(w))
		})
	}
}

func TestProfileConsoleWithoutVTIsPlain(t *testing.T) {
	withEnv(t, "COLORTERM=truecolor")
	tty := ttyWriter(t)
	consoleVT = func(io.Writer) bool { return false }
	assert.Equal(t, profilePlain, defaultIO.profileFor(tty))
}

func TestProfileConsoleVTNotCalledForPipe(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	prev := consoleVT
	consoleVT = func(io.Writer) bool {
		t.Fatal("pipe must not reach SetConsoleMode")
		return false
	}
	t.Cleanup(func() { consoleVT = prev })
	assert.Equal(t, profileTrueColor, defaultIO.profileFor(&bytes.Buffer{}))
}

func TestProfileForManagedWriterUsesRawOutput(t *testing.T) {
	withEnv(t, "COLORTERM=truecolor")
	tty := ttyWriter(t)
	managed := &tio{writer: tty, env: defaultIO.env}
	assert.Equal(t, profileTrueColor, managed.profileFor(&chanIO{}))
	piped := &tio{writer: &bytes.Buffer{}, env: defaultIO.env}
	assert.Equal(t, profilePlain, piped.profileFor(&chanIO{}))
}
