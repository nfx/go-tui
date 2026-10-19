// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type filePicker struct {
	extensions map[string]bool
	start      string
	showHidden bool
	ignoreDirs bool
	ignoreUp   bool
}

var upEntry = &dirEntry{name: "..", isDir: true}

func (f *filePicker) list(dir string) ([]os.DirEntry, error) {
	slog.Debug("list", "dir", dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var out []os.DirEntry
	if !f.ignoreUp && dir != f.start {
		out = append(out, upEntry)
	}
	for _, e := range entries {
		if f.skip(e) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (f *filePicker) skip(e os.DirEntry) bool {
	name := e.Name()
	if len(f.extensions) > 0 && !e.IsDir() {
		ext := strings.ToLower(filepath.Ext(name))
		if !f.extensions[ext] {
			return true
		}
	}
	if !f.showHidden && name[0] == '.' {
		return true
	}
	if f.ignoreDirs && e.IsDir() {
		return true
	}
	return false
}

func WithExtensions(exts ...string) opt {
	return opT(func(f *filePicker) error {
		for _, ext := range exts {
			f.extensions[strings.ToLower(ext)] = true
		}
		return nil
	})
}

func WithStartDir(dir string) opt {
	return opT(func(f *filePicker) error {
		f.start = dir
		return nil
	})
}

func WithIgnoreUp() opt {
	return opT(func(f *filePicker) error {
		f.ignoreUp = true
		return nil
	})
}

func WithIgnoreDirs() opt {
	return opT(func(f *filePicker) error {
		f.ignoreDirs = true
		return nil
	})
}

func WithShowHidden() opt {
	return opT(func(f *filePicker) error {
		f.showHidden = true
		return nil
	})
}

// newFilePicker applies o to the picker and to the dropdown that shows the
// first directory. Every option must be supported by at least one of them.
func newFilePicker(ctx context.Context, o ...opt) (*filePicker, *dropdown, error) {
	f := &filePicker{
		extensions: map[string]bool{},
	}
	d := newDropdown(ctx)
	for _, option := range o {
		err := applyToAny(option, f, d)
		if err != nil {
			return nil, nil, err
		}
	}
	if f.start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, nil, fmt.Errorf("pwd: %w", err)
		}
		f.start = wd
	}
	return f, d, nil
}

func FilePicker(ctx context.Context, title string, o ...opt) (string, error) {
	f, d, err := newFilePicker(ctx, o...)
	if err != nil {
		return "", err
	}
	stack := []os.DirEntry{
		&dirEntry{name: f.start, isDir: true},
	}
	terminal := outputIsTerminal(d.out)
	if !terminal && !d.canAnswer() {
		// nothing can answer, so no directory is walked
		return "", errNoDefault(title)
	}
	visited := walked{}
	for stack[len(stack)-1].IsDir() {
		dirName := joinStack(stack)
		if !terminal && visited.revisits(dirName) {
			return "", errBadDefault("walks %s again", dirName)
		}
		entries, err := f.list(dirName)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return "", ErrNoItems
		}
		if d == nil {
			d, err = f.nextDropdown(ctx, o)
			if err != nil {
				return "", err
			}
		}
		items := make([]any, len(entries))
		for i, e := range entries {
			items[i] = e
		}
		i, err := f.choose(ctx, d, title, entries, items)
		if err != nil {
			return "", err
		}
		d = nil
		entry := entries[i]
		if entry.Name() == ".." {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if !entry.IsDir() {
			return filepath.Join(dirName, entry.Name()), nil
		}
		stack = append(stack, entry)
	}
	return stack[len(stack)-1].Name(), nil
}

// nextDropdown configures the dropdown of the next directory. Options were
// validated by newFilePicker, so the bundle may only skip the ones that
// configure the picker itself.
func (f *filePicker) nextDropdown(ctx context.Context, o []opt) (*dropdown, error) {
	d := newDropdown(ctx)
	err := WithOptions(o...)(d)
	if err != nil && !errors.Is(err, ErrWrongWidget) {
		return nil, err
	}
	return d, nil
}

// canAnswer reports whether a default or the single-entry shortcut is set.
func (d *dropdown) canAnswer() bool { return d.def.set || d.OneReturn }

// joinStack is the path of the directory on top of the stack.
func joinStack(stack []os.DirEntry) string {
	parts := make([]string, len(stack))
	for i, e := range stack {
		parts[i] = e.Name()
	}
	return filepath.Join(parts...)
}

// walked remembers the directories visited without a terminal.
type walked map[string]bool

// revisits records dir and reports whether it was visited before.
func (w walked) revisits(dir string) bool {
	again := w[dir]
	w[dir] = true
	return again
}

// choose asks for an entry of one directory, or answers from the default.
func (f *filePicker) choose(
	ctx context.Context,
	d *dropdown,
	title string,
	entries []os.DirEntry,
	items []any,
) (int, error) {
	if outputIsTerminal(d.out) {
		return d.pick(ctx, title, items)
	}
	return f.answerFromDefault(ctx, d, title, entries, items)
}

// soleEntry returns the only entry that is not the way up.
func (f *filePicker) soleEntry(entries []os.DirEntry) (int, bool) {
	found := -1
	for i, e := range entries {
		if e == os.DirEntry(upEntry) {
			continue
		}
		if found >= 0 {
			return -1, false
		}
		found = i
	}
	return found, found >= 0
}

// answerFromDefault picks an entry of one directory without a terminal. A
// string names an entry, exactly or as a prefix hint; an [os.DirEntry] is
// identified by name and kind. The entries are already filtered.
func (f *filePicker) answerFromDefault(
	ctx context.Context,
	d *dropdown,
	title string,
	entries []os.DirEntry,
	items []any,
) (int, error) {
	err := ctx.Err()
	if err != nil {
		return -1, err
	}
	d.Label = title
	d.Items = items
	err = d.parseTemplates()
	if err != nil {
		return -1, fmt.Errorf("templates: %w", err)
	}
	if i, ok := f.soleEntry(entries); ok && d.OneReturn {
		return i, nil
	}
	if !d.def.set {
		return -1, errNoDefault(title)
	}
	var matches []int
	switch want := d.def.raw.(type) {
	case os.DirEntry:
		for i, e := range entries {
			if e.Name() == want.Name() && e.IsDir() == want.IsDir() {
				matches = append(matches, i)
			}
		}
		i, err := uniqueMatch(matches, fmt.Sprintf("entry %q", want.Name()))
		if err != nil {
			return -1, err
		}
		return i, ctx.Err()
	case string:
		for i, e := range entries {
			if e.Name() == want {
				matches = append(matches, i)
			}
		}
		if len(matches) == 0 {
			matches, err = d.hintMatches(items)(want)
			if err != nil {
				return -1, err
			}
		}
		i, err := uniqueMatch(matches, fmt.Sprintf("name %q", want))
		if err != nil {
			return -1, err
		}
		return i, ctx.Err()
	}
	return -1, errBadDefault("%T is neither a name nor an os.DirEntry", d.def.raw)
}

type dirEntry struct {
	name  string
	isDir bool
}

func (d *dirEntry) String() string {
	if d.name == ".." {
		return ".."
	}
	if d.isDir {
		return "d " + d.name + string(os.PathSeparator)
	}
	return "- " + d.name + string(os.PathSeparator)
}

func (d *dirEntry) Name() string {
	return d.name
}

func (d *dirEntry) IsDir() bool {
	return d.isDir
}

func (d *dirEntry) Type() os.FileMode {
	if d.isDir {
		return os.ModeDir
	}
	return 0
}

func (d *dirEntry) Info() (os.FileInfo, error) {
	return nil, errors.New("not implemented")
}
