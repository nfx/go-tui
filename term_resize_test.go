// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestTermIORefreshSizeDirectTTY(t *testing.T) {
	origGet := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = origGet })
	tio := &termIO{
		Width:  80,
		Height: 24,
		fd:     2, // stderr
	}
	tio.refreshSize()
	assert.Equal(t, 120, tio.Width)
	assert.Equal(t, 40, tio.Height)
}

func TestTermIORefreshSizeNoFd(t *testing.T) {
	tio := &termIO{Width: 80, Height: 24, fd: 0}
	tio.refreshSize()
	// should not change when fd < 1
	assert.Equal(t, 80, tio.Width)
	assert.Equal(t, 24, tio.Height)
}

func TestTermIORefreshSizeWithChanIO(t *testing.T) {
	origGet := termGetSize
	termGetSize = func(int) (int, int, error) { return 100, 50, nil }
	t.Cleanup(func() { termGetSize = origGet })
	cio := &chanIO{fd: 2, width: 80, height: 24}
	tio := &termIO{Width: 80, Height: 24, cio: cio}
	tio.refreshSize()
	assert.Equal(t, 100, tio.Width)
	assert.Equal(t, 50, tio.Height)
}

func TestTermIORefreshSizeChanIONoFd(t *testing.T) {
	cio := &chanIO{fd: 0}
	tio := &termIO{Width: 80, Height: 24, cio: cio}
	tio.refreshSize()
	assert.Equal(t, 80, tio.Width)
}

func TestViewportWriteWithWidthUpdatesWidth(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	notify := make(chan viewportChanged, 10)
	vp := initViewport(ctx, notify, 40, 10)
	// write content at new width
	_, err := vp.writeWithWidth([]byte("hello\n"), 80)
	assert.NoError(t, err)
	<-notify
	// verify the viewport width was updated
	var buf bytes.Buffer
	_, err = vp.WriteTo(&buf)
	assert.NoError(t, err)
	// the line should be padded to the new width (80)
	assert.True(t, len(buf.Bytes()) > 40)
}

func TestChanIOClearsWrappedManagedRowsAfterWidthShrink(t *testing.T) {
	cio, stdout := chainIOforTest(t, 40, 6)
	vp, err := cio.pushViewport()
	assert.NoError(t, err)

	const wideLine = "123456789012345678901234567890123456789"
	_, err = vp.writeWithWidth([]byte(wideLine+"\n\r"), 40)
	assert.NoError(t, err)
	_ = waitOutputContains(t, stdout.C, wideLine)

	cio.sizeMu.Lock()
	cio.width = 10
	cio.sizeMu.Unlock()
	_, err = vp.writeWithWidth([]byte("short\n\r"), 10)
	assert.NoError(t, err)
	out := waitOutputContains(t, stdout.C, "short")

	assert.Equal(t, 4, strings.Count(out, "\x1b[K"))
	assert.Equal(t, 3, strings.Count(out, "\x1b[1A"))
}
