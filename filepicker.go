// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

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
		entry, err := f.step(ctx, d, title, dirName, entries)
		if err != nil {
			return "", err
		}
		if !entry.IsDir() {
			path := filepath.Join(dirName, entry.Name())
			err = f.answer(d, title, path)
			if err != nil {
				return "", err
			}
			return path, nil
		}
		d = nil
		if entry.Name() == ".." {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		stack = append(stack, entry)
	}
	return stack[len(stack)-1].Name(), nil
}
