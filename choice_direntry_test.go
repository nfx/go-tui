// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"os"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestDirEntryString(t *testing.T) {
	assert.Equal(t, "..", (&dirEntry{name: "..", isDir: true}).String())
	assert.Equal(t, "d folder"+string(os.PathSeparator), (&dirEntry{name: "folder", isDir: true}).String())
	assert.Equal(t, "- file"+string(os.PathSeparator), (&dirEntry{name: "file", isDir: false}).String())
}
