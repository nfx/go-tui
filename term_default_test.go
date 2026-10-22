// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"github.com/nfx/go-tui/internal/color"
	"io"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

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

func TestProfileFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		tty  bool
		want color.Profile
	}{
		{"tty truecolor", []string{"COLORTERM=truecolor"}, true, color.ProfileTrueColor},
		{"tty 24bit", []string{"COLORTERM=24bit"}, true, color.ProfileTrueColor},
		{"tty 256", []string{"TERM=xterm-256color"}, true, color.ProfileANSI256},
		{"tty bare", nil, true, color.ProfileANSI16},
		{"tty windows terminal", []string{"WT_SESSION=x"}, true, color.ProfileTrueColor},
		{"pipe force 3", []string{"FORCE_COLOR=3"}, false, color.ProfileTrueColor},
		{"pipe force 2", []string{"FORCE_COLOR=2"}, false, color.ProfileANSI256},
		{"pipe force 1", []string{"FORCE_COLOR=1"}, false, color.ProfileANSI16},
		{"pipe force empty", []string{"FORCE_COLOR="}, false, color.ProfileANSI16},
		{"pipe force garbage", []string{"FORCE_COLOR=banana"}, false, color.ProfileANSI16},
		{"tty force 0", []string{"FORCE_COLOR=0"}, true, color.ProfilePlain},
		{"tty force false", []string{"FORCE_COLOR=false"}, true, color.ProfilePlain},
		{"unknown non-tty", nil, false, color.ProfilePlain},
		{"clicolor force", []string{"CLICOLOR_FORCE=1"}, false, color.ProfileANSI16},
		{"clicolor force 0", []string{"CLICOLOR_FORCE=0"}, false, color.ProfilePlain},
		{"clicolor force depth", []string{"CLICOLOR_FORCE=1", "COLORTERM=truecolor"}, false, color.ProfileTrueColor},
		{"no color tty", []string{"NO_COLOR=1"}, true, color.ProfileAttrs},
		{"no color pipe", []string{"NO_COLOR=1"}, false, color.ProfilePlain},
		{"force beats no color", []string{"NO_COLOR=1", "FORCE_COLOR=3"}, true, color.ProfileTrueColor},
		{"dumb", []string{"TERM=dumb"}, true, color.ProfilePlain},
		{"ci tty", []string{"CI=true", "COLORTERM=truecolor"}, true, color.ProfileAttrs},
		{"ci pipe", []string{"CI=true"}, false, color.ProfilePlain},
		{"ci 1", []string{"CI=1"}, true, color.ProfileAttrs},
		{"ci false", []string{"CI=false"}, true, color.ProfileANSI16},
		{"ci 0", []string{"CI=0"}, true, color.ProfileANSI16},
		{"ci clicolor force", []string{"CI=true", "CLICOLOR_FORCE=1"}, false, color.ProfileANSI16},
		{"ci force color", []string{"CI=true", "FORCE_COLOR=3"}, false, color.ProfileTrueColor},
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
	assert.Equal(t, color.ProfilePlain, defaultIO.profileFor(tty))
}

func TestProfileConsoleVTNotCalledForPipe(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	prev := consoleVT
	consoleVT = func(io.Writer) bool {
		t.Fatal("pipe must not reach SetConsoleMode")
		return false
	}
	t.Cleanup(func() { consoleVT = prev })
	assert.Equal(t, color.ProfileTrueColor, defaultIO.profileFor(&bytes.Buffer{}))
}

func TestProfileForManagedWriterUsesRawOutput(t *testing.T) {
	withEnv(t, "COLORTERM=truecolor")
	tty := ttyWriter(t)
	managed := &tio{writer: tty, env: defaultIO.env}
	assert.Equal(t, color.ProfileTrueColor, managed.profileFor(&chanIO{}))
	piped := &tio{writer: &bytes.Buffer{}, env: defaultIO.env}
	assert.Equal(t, color.ProfilePlain, piped.profileFor(&chanIO{}))
}
