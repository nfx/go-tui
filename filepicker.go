// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
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

func opT[T any](o func(d *T) error) opt {
	return func(raw any) error {
		var zero T
		concrete, ok := raw.(*T)
		if !ok {
			return fmt.Errorf("%w: need a %T, got %T", ErrWrongWidget, zero, raw)
		}
		return o(concrete)
	}
}

func newFilePicker(o ...opt) (*filePicker, error) {
	f := &filePicker{
		extensions: map[string]bool{},
	}
	err := opts(o).Apply(f)
	if err != nil {
		return nil, err
	}
	if f.start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("pwd: %w", err)
		}
		f.start = wd
	}
	return f, nil
}

func FilePicker(title string, o ...opt) (string, error) {
	f, err := newFilePicker(o...)
	if err != nil {
		return "", err
	}
	dropdownOpts := append([]opt{
		// WithOneReturn(),
	}, o...)
	stack := []os.DirEntry{
		&dirEntry{name: f.start, isDir: true},
	}
	for stack[len(stack)-1].IsDir() {
		parts := []string{}
		for _, e := range stack {
			parts = append(parts, e.Name())
		}
		dirName := filepath.Join(parts...)
		entries, err := f.list(dirName)
		if err != nil {
			return "", err
		}
		entry, err := Dropdown(title, entries, dropdownOpts...)
		if err != nil {
			return "", err
		}
		if entry.Name() == ".." {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if !entry.IsDir() {
			parts = append(parts, entry.Name())
			return filepath.Join(parts...), nil
		}
		stack = append(stack, entry)
	}
	return stack[len(stack)-1].Name(), nil
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
