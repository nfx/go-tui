// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/term"
)

type filePicker struct {
	extensions map[string]bool
	start      string
	showHidden bool
	ignoreDirs bool
	ignoreUp   bool

	// theme is captured once per invocation for the dropdowns of every
	// directory; look is the binding of the latest one that bound.
	theme *render.Compiled
	look  render.Binding
}

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

// newFilePicker applies o to the picker and to the dropdown that shows the
// first directory. Every option must be supported by at least one of them.
func newFilePicker(ctx context.Context, o ...option.Opt) (*filePicker, *dropdown, error) {
	f := &filePicker{
		extensions: map[string]bool{},
	}
	d := newDropdown(ctx)
	f.theme = d.theme
	f.adopt(d)
	for _, one := range o {
		err := option.ApplyToAny(one, f, d)
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

// step asks for one of the entries of dir, keeping the binding of d for the
// dropdown of the next directory.
func (f *filePicker) step(
	ctx context.Context,
	d *dropdown,
	title, dir string,
	entries []os.DirEntry,
) (os.DirEntry, error) {
	items := make([]any, len(entries))
	for i, e := range entries {
		items[i] = e
	}
	d.view = f.viewIn(dir)
	i, err := f.choose(ctx, d, title, entries, items)
	if err != nil {
		return nil, err
	}
	if d.look.Theme != nil {
		f.look = d.look
	}
	return entries[i], nil
}

// nextDropdown configures the dropdown of the next directory. Options were
// validated by newFilePicker, so the dropdown may only skip the ones that
// configure the picker itself.
func (f *filePicker) nextDropdown(ctx context.Context, o []option.Opt) (*dropdown, error) {
	d := newDropdown(ctx)
	f.adopt(d)
	for _, one := range o {
		err := one(d)
		if err != nil && !errors.Is(err, errs.ErrWrongWidget) {
			return nil, err
		}
	}
	return d, nil
}

// adopt makes d render through the captured theme, the FilePicker slots and
// the binding of the previous directory, so that navigation never switches
// appearance.
func (f *filePicker) adopt(d *dropdown) {
	d.theme, d.inherit = f.theme, f.look
	d.family = f.theme.Templates.FilePicker
}

// viewIn presents the entries of dir to the theme slots.
func (f *filePicker) viewIn(dir string) func(any) any {
	return func(item any) any {
		e, ok := item.(os.DirEntry)
		if !ok {
			return item // the picker lists nothing else
		}
		return fileView{
			Name:   e.Name(),
			Path:   filepath.Join(dir, e.Name()),
			IsDir:  e.IsDir(),
			Parent: e == os.DirEntry(upEntry),
		}
	}
}

// fileView is the payload of the FilePicker Value and Details slots: one
// entry of the listed directory, whatever its type and String method. Path
// joins the directory and the name; Parent marks the way up.
type fileView struct {
	Name, Path    string
	IsDir, Parent bool
}

// answer retains the chosen path once, after every directory dropdown has
// cleared. Without a terminal nothing is retained.
func (f *filePicker) answer(d *dropdown, title, path string) error {
	if !term.OutputIsTerminal(d.out) {
		return nil
	}
	err := d.retain(title, path)
	if err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	return nil
}

// canAnswer reports whether a default or the single-entry shortcut is set.
func (d *dropdown) canAnswer() bool { return d.def.Set || d.OneReturn }

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
	if !term.OutputIsTerminal(d.out) {
		return f.answerFromDefault(ctx, d, title, entries, items)
	}
	// the path is retained once, by answer
	d.Label, d.Items = title, items
	return d.dropdownIndex(ctx)
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
	if !d.def.Set {
		return -1, option.NoDefault(title)
	}
	var matches []int
	switch want := d.def.Raw.(type) {
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
	return -1, option.BadDefault("%T is neither a name nor an os.DirEntry", d.def.Raw)
}
