// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/terst"
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

func TestFilePickerDefaults(t *testing.T) {
	terst.Quiet(t)
	ctx := t.Context()
	root := t.TempDir()
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	for _, name := range []string{"main.go", "main_test.go", "sub/inner.txt", "notes.md"} {
		assert.NoError(t, os.WriteFile(filepath.Join(root, name), nil, 0o600))
	}
	dir := WithStartDir(root)

	got, err := FilePicker(ctx, "File", dir, option.WithDefault("notes.md"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "notes.md"), got)

	// exact beats prefix
	got, err = FilePicker(ctx, "File", dir, option.WithDefault("main.go"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "main.go"), got)

	got, err = FilePicker(ctx, "File", dir, option.WithDefault("notes"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "notes.md"), got)

	_, err = FilePicker(ctx, "File", dir, option.WithDefault("main"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = FilePicker(ctx, "File", dir, option.WithDefault("missing"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = FilePicker(ctx, "File", dir, option.WithDefault(42))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = FilePicker(ctx, "File", dir)
	assert.ErrorIs(t, err, errs.ErrNoTTY)

	// filters apply before resolution
	_, err = FilePicker(ctx, "File", dir, WithExtensions(".md"), option.WithDefault("main.go"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)

	// a typed directory entry continues the traversal; it never returns a directory
	entries, err := os.ReadDir(root)
	assert.NoError(t, err)
	var sub os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			sub = e
		}
	}
	_, err = FilePicker(ctx, "File", dir, option.WithDefault(sub))
	assert.ErrorIs(t, err, errs.ErrInvalidOption) // the same default is missing inside of sub

	// singleton navigation
	only := t.TempDir()
	assert.NoError(t, os.MkdirAll(filepath.Join(only, "a", "b"), 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(only, "a", "b", "f.txt"), nil, 0o600))
	got, err = FilePicker(ctx, "File", WithStartDir(only), WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(only, "a", "b", "f.txt"), got)

	_, err = FilePicker(ctx, "File", WithStartDir(filepath.Join(only, "gone")), WithOneReturn())
	assert.Error(t, err)

	// a cycle is rejected instead of looping
	_, err = FilePicker(ctx, "File", WithStartDir(only), option.WithDefault(".."))
	assert.Error(t, err)
}

// pickerTree is a start directory with a.txt and sub/inner.txt.
func pickerTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	assert.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	for _, name := range []string{"a.txt", "sub/inner.txt"} {
		assert.NoError(t, os.WriteFile(filepath.Join(root, name), nil, 0o600))
	}
	return root
}

// clearPicker erases one directory frame of three rows below the label.
const clearPicker = "\r\x1b[4A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[3A\r"

func TestThemedFilePickerNavigatesAndRetainsPathOnce(t *testing.T) {
	root := pickerTree(t)
	o, out, restored := themedDropdownOpt(t, terst.ASCIITheme(), 40, "\x1b[B", "\r", "\x1b[B", "\r")
	got, err := FilePicker(t.Context(), "File", WithStartDir(root), o)
	assert.NoError(t, err)
	want := filepath.Join(root, "sub", "inner.txt")
	assert.Equal(t, want, got)
	assert.True(t, *restored)
	hint := "\r| ^v move · type filter\n"
	// each directory erases its frame once and retains nothing; the chosen
	// path is retained once, as the answer of the whole invocation
	assert.Equal(t, ""+
		"\r? File\n\r| > a.txt\n\r|   sub/\n"+hint+clearPicker+
		"\r? File\n\r|   a.txt\n\r| > sub/\n"+hint+clearPicker+
		"\r? File\n\r| > ..\n\r|   inner.txt\n"+hint+clearPicker+
		"\r? File\n\r|   ..\n\r| > inner.txt\n"+hint+clearPicker+
		"+ File · "+want+"\n", out.String())
}

func TestThemedFilePickerParentEntryAndFileView(t *testing.T) {
	root := pickerTree(t)
	th := terst.ASCIITheme()
	th.Funcs = template.FuncMap{"under": func(p string) string { return strings.TrimPrefix(p, root) }}
	th.FilePicker.Value = `{{ if .Parent }}up{{ else }}{{ under .Path }}{{ if .IsDir }} dir{{ end }}{{ end }}`
	o, out, _ := themedDropdownOpt(t, th, 60, "\x1b[B", "\r", "\r", "\r")
	got, err := FilePicker(t.Context(), "File", WithStartDir(root), o)
	assert.NoError(t, err)
	// into sub, back up through the parent entry, then the first file
	assert.Equal(t, filepath.Join(root, "a.txt"), got)
	sep := string(os.PathSeparator)
	assert.Contains(t, out.String(), "\r| > "+sep+"a.txt\n\r|   "+sep+"sub dir\n")
	assert.Contains(t, out.String(), "\r| > up\n\r|   "+filepath.Join(sep+"sub", "inner.txt")+"\n")
	// the answer is the returned path, not the projection
	assert.True(t, strings.HasSuffix(out.String(), "+ File · "+got+"\n"))
}

func TestThemedFilePickerMutesParentEntry(t *testing.T) {
	terst.WithEnv(t, "FORCE_COLOR=3")
	th := terst.ASCIITheme()
	th.Light = true
	o, out, _ := themedDropdownOpt(t, th, 40, "\x1b[B", "\r", "\x1b[B", "\r")
	_, err := FilePicker(t.Context(), "File", WithStartDir(pickerTree(t)), o)
	assert.NoError(t, err)
	roles := term.Default.BindTheme(t.Context(), term.Default.CurrentTheme(), out, nil).Roles
	assert.True(t, len(roles.Muted) > 0)
	assert.Contains(t, out.String(), string(roles.Muted)+"..")
	assert.True(t, !strings.Contains(out.String(), string(roles.Muted)+"inner.txt"))
}

func TestThemedFilePickerKeepsOneThemeAcrossDirectories(t *testing.T) {
	root := pickerTree(t)
	o, out, _ := themedDropdownOpt(t, terst.ASCIITheme(), 40, "\x1b[B", "\r", "\x1b[B", "\r")
	other := terst.ASCIITheme()
	other.Prompt.Label = `other {{ .Label }}`
	other.Prompt.Answer = `other {{ .Value }}`
	terminals := 0
	install := option.OpT(func(d *dropdown) error {
		next := d.makeTermIO
		d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			terminals++
			if terminals == 2 {
				// a new default between directories reaches later invocations only
				assert.NoError(t, term.Default.InstallTheme(other))
			}
			return next(in, out)
		}
		return nil
	})
	_, err := FilePicker(t.Context(), "File", WithStartDir(root), o, install)
	assert.NoError(t, err)
	assert.Equal(t, 2, terminals)
	assert.True(t, !strings.Contains(out.String(), "other"))
	// two frames in each directory, all through the captured theme
	assert.Equal(t, 4, strings.Count(out.String(), "\r? File\n"))
}

func TestFilePickerNextDropdownInheritsThemeAndBinding(t *testing.T) {
	terst.UseASCII(t)
	f, first, err := newFilePicker(t.Context(), WithStartDir(t.TempDir()))
	assert.NoError(t, err)
	f.look = term.Default.BindTheme(t.Context(), f.theme, first.out, nil)
	inline := terst.ASCIITheme()
	inline.Inline = true
	assert.NoError(t, term.Default.InstallTheme(inline))
	next, err := f.nextDropdown(t.Context(), nil)
	assert.NoError(t, err)
	assert.True(t, next.theme == f.theme)
	assert.True(t, next.inherit.Theme == f.theme)
	assert.True(t, next.destination(t.Context(), nil).Theme == f.theme)
	assert.True(t, next.family.Value == f.theme.Templates.FilePicker.Value)
}

func TestThemedFilePickerSlotPrecedence(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o600))
	run := func(th theme.Theme) string {
		t.Helper()
		o, out, _ := themedDropdownOpt(t, th, 40, "\r")
		_, err := FilePicker(t.Context(), "File", WithStartDir(dir), o)
		assert.NoError(t, err)
		return out.String()
	}
	// a shared projection reaches the picker's entries
	th := terst.ASCIITheme()
	th.Choice.Value = `{{ .Name }}!`
	th.Prompt.Answer = `{{ .Label }}={{ .Value }}`
	th.Dropdown.Label = `dropdown {{ .Label }}`
	th.Dropdown.Value = `dropdown`
	got := run(th)
	assert.True(t, strings.HasPrefix(got, "\r? File\n\r| > a.txt!\n"))
	assert.True(t, strings.HasSuffix(got, "File="+filepath.Join(dir, "a.txt")+"\n"))

	// the family wins locally; Hidden hides the hint
	th.FilePicker.Value = `[{{ .Name }}]`
	th.FilePicker.Label = `pick {{ .Label }}`
	th.FilePicker.Hint = theme.Hidden
	got = run(th)
	assert.True(t, strings.HasPrefix(got, "\rpick File\n\r| > [a.txt]\n\r\x1b[2A"))
}

func TestThemedFilePickerHiddenAnswer(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o600))
	th := terst.ASCIITheme()
	th.FilePicker.Answer = theme.Hidden
	o, out, _ := themedDropdownOpt(t, th, 40, "\r")
	_, err := FilePicker(t.Context(), "File", WithStartDir(dir), o)
	assert.NoError(t, err)
	// the frame is erased and nothing is retained
	assert.True(t, strings.HasSuffix(out.String(), "| ^v move · type filter\n\r"+
		"\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r"))

	o, out, _ = themedDropdownOpt(t, terst.ASCIITheme(), 40, "\r")
	_, err = FilePicker(t.Context(), "File", WithStartDir(dir), o, option.WithHide())
	assert.NoError(t, err)
	assert.True(t, !strings.Contains(out.String(), "+ File"))
}

func TestThemedFilePickerDefaultsWithoutTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := terst.ASCIITheme()
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	// without a terminal only the projection runs, never a presentation slot
	th.Prompt.Label = `{{ fails .Label }}`
	th.FilePicker.Active = `{{ fails .Value }}`
	th.FilePicker.Inactive = `{{ fails .Value }}`
	th.FilePicker.Answer = `{{ fails .Value }}`
	terst.UseTheme(t, th)
	terst.Quiet(t)
	ctx := t.Context()
	root := t.TempDir()
	assert.NoError(t, os.Mkdir(filepath.Join(root, "notes"), 0o755))
	for _, name := range []string{"main.go", "notes.md", "notes/n.txt"} {
		assert.NoError(t, os.WriteFile(filepath.Join(root, name), nil, 0o600))
	}
	dir := WithStartDir(root)
	got, err := FilePicker(ctx, "File", dir, option.WithDefault("main"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "main.go"), got)
	// the exact name of an entry wins over the hint
	got, err = FilePicker(ctx, "File", dir, option.WithDefault("notes.md"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "notes.md"), got)
	// "notes" names the directory, and is missing inside of it
	_, err = FilePicker(ctx, "File", dir, option.WithDefault("notes"))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	_, err = FilePicker(ctx, "File", dir)
	assert.ErrorIs(t, err, errs.ErrNoTTY)

	// hints match the FilePicker projection
	th.FilePicker.Value = `x{{ .Name }}`
	terst.UseTheme(t, th)
	got, err = FilePicker(ctx, "File", dir, option.WithDefault("xma"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "main.go"), got)
}

func TestThemedFilePickerUnreadableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("permissions are not enforced")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	assert.NoError(t, os.Mkdir(locked, 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), nil, 0o600))
	assert.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { assert.NoError(t, os.Chmod(locked, 0o755)) })
	o, out, restored := themedDropdownOpt(t, terst.ASCIITheme(), 40, "\x1b[B", "\r")
	_, err := FilePicker(t.Context(), "File", WithStartDir(root), o)
	assert.ErrorIs(t, err, os.ErrPermission)
	assert.True(t, *restored)
	assert.True(t, strings.HasSuffix(out.String(), clearPicker))
}

func TestThemedFilePickerHelperErrorRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := terst.ASCIITheme()
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.FilePicker.Inactive = `{{ fails .Value }}`
	o, out, restored := themedDropdownOpt(t, th, 40, "\r")
	_, err := FilePicker(t.Context(), "File", WithStartDir(pickerTree(t)), o)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ File"))
}

func TestFilePickerSplitsOptionsBetweenParts(t *testing.T) {
	terst.UseTerminalInput(t, nil)
	_, writer := term.DefaultStreams(t.Context())
	f, d, err := newFilePicker(t.Context(),
		WithStartDir(t.TempDir()),
		WithShowHidden(),
		option.WithHide(),
	)
	assert.NoError(t, err)
	assert.True(t, f.showHidden)
	assert.True(t, d.Hide)
	assert.Equal(t, writer, d.out)
}
