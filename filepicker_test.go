// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestFilePickerListFilters(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("ok"), 0o644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "drop.log"), []byte("noop"), 0o644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, ".hidden.txt"), []byte("hidden"), 0o644)
	assert.NoError(t, err)
	err = os.Mkdir(filepath.Join(dir, "nested"), 0o755)
	assert.NoError(t, err)

	fp := &filePicker{
		extensions: map[string]bool{".txt": true},
		start:      dir,
	}

	entries, err := fp.list(dir)
	assert.NoError(t, err)

	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	assert.Equal(t, map[string]bool{
		"keep.txt": true,
		"nested":   true,
	}, names)
}

func TestFilePickerListsUpEntry(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	err := os.Mkdir(child, 0o755)
	assert.NoError(t, err)

	fp := &filePicker{
		extensions: map[string]bool{},
		start:      root,
	}

	entries, err := fp.list(child)
	assert.NoError(t, err)
	assert.True(t, len(entries) > 0)
	assert.Equal(t, "..", entries[0].Name())
}

func TestFilePickerSkipRules(t *testing.T) {
	fp := &filePicker{
		extensions: map[string]bool{},
		showHidden: false,
		ignoreDirs: true,
	}

	assert.True(t, fp.skip(&dirEntry{name: ".hidden"}))
	assert.True(t, fp.skip(&dirEntry{name: "dir", isDir: true}))

	fp.extensions[".txt"] = true
	assert.True(t, fp.skip(&dirEntry{name: "note.md"}))
	assert.True(t, !fp.skip(&dirEntry{name: "keep.txt"}))
}

func TestDirEntryString(t *testing.T) {
	assert.Equal(t, "..", (&dirEntry{name: "..", isDir: true}).String())
	assert.Equal(t, "d folder"+string(os.PathSeparator), (&dirEntry{name: "folder", isDir: true}).String())
	assert.Equal(t, "- file"+string(os.PathSeparator), (&dirEntry{name: "file", isDir: false}).String())
}

func TestFilePickerDefaults(t *testing.T) {
	io := quiet(t)
	ctx := t.Context()
	root := t.TempDir()
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	for _, name := range []string{"main.go", "main_test.go", "sub/inner.txt", "notes.md"} {
		assert.NoError(t, os.WriteFile(filepath.Join(root, name), nil, 0o600))
	}
	dir := WithStartDir(root)

	got, err := FilePicker(ctx, "File", io, dir, WithDefault("notes.md"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "notes.md"), got)

	// exact beats prefix
	got, err = FilePicker(ctx, "File", io, dir, WithDefault("main.go"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "main.go"), got)

	got, err = FilePicker(ctx, "File", io, dir, WithDefault("notes"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "notes.md"), got)

	_, err = FilePicker(ctx, "File", io, dir, WithDefault("main"))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = FilePicker(ctx, "File", io, dir, WithDefault("missing"))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = FilePicker(ctx, "File", io, dir, WithDefault(42))
	assert.ErrorIs(t, err, ErrInvalidOption)
	_, err = FilePicker(ctx, "File", io, dir)
	assert.ErrorIs(t, err, ErrNoTTY)

	// filters apply before resolution
	_, err = FilePicker(ctx, "File", io, dir, WithExtensions(".md"), WithDefault("main.go"))
	assert.ErrorIs(t, err, ErrInvalidOption)

	// a typed directory entry continues the traversal; it never returns a directory
	entries, err := os.ReadDir(root)
	assert.NoError(t, err)
	var sub os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			sub = e
		}
	}
	_, err = FilePicker(ctx, "File", io, dir, WithDefault(sub))
	assert.ErrorIs(t, err, ErrInvalidOption) // the same default is missing inside of sub

	// singleton navigation
	only := t.TempDir()
	assert.NoError(t, os.MkdirAll(filepath.Join(only, "a", "b"), 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(only, "a", "b", "f.txt"), nil, 0o600))
	got, err = FilePicker(ctx, "File", io, WithStartDir(only), WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(only, "a", "b", "f.txt"), got)

	_, err = FilePicker(ctx, "File", io, WithStartDir(filepath.Join(only, "gone")), WithOneReturn())
	assert.Error(t, err)

	// a cycle is rejected instead of looping
	_, err = FilePicker(ctx, "File", io, WithStartDir(only), WithDefault(".."))
	assert.Error(t, err)
}
