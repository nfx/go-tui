// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/theme"
	"strings"
	"sync"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/assert"
)

func TestInstallAtomicAndIsolation(t *testing.T) {
	prev := defaultIO.currentTheme()
	t.Cleanup(func() { defaultIO.theme.Store(prev) })

	th := theme.New()
	th.Palette.Accent = "#112233"
	assert.NoError(t, defaultIO.installTheme(th))
	c := defaultIO.currentTheme()
	assert.Equal(t, theme.RGB("#112233"), c.Editable().Palette.Accent)

	// Mutating the caller's value and the returned copy leaves the snapshot alone.
	th.Palette.Accent = "#445566"
	got := defaultIO.currentTheme().Editable()
	got.Palette.Accent = "#778899"
	assert.Equal(t, theme.RGB("#112233"), c.Source.Palette.Accent)
	assert.Equal(t, theme.RGB("#112233"), defaultIO.currentTheme().Editable().Palette.Accent)

	// A failed installation changes nothing.
	bad := theme.New()
	bad.Palette.Accent = "nope"
	assert.Error(t, defaultIO.installTheme(bad))
	assert.True(t, defaultIO.currentTheme() == c)
}

func TestConcurrentInstall(t *testing.T) {
	prev := defaultIO.currentTheme()
	t.Cleanup(func() { defaultIO.theme.Store(prev) })
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			th := theme.New()
			if i%2 == 0 {
				th.Palette.Accent = "nope"
			}
			err := defaultIO.installTheme(th)
			if i%2 == 0 {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			if c := defaultIO.currentTheme(); c == nil {
				t.Error("torn theme")
			}
		})
	}
	wg.Wait()
}

func TestWithFnRegistersForFutureOperations(t *testing.T) {
	before := keepTheme(t)
	WithFn("shout", func(s string) string { return strings.ToUpper(s) + "!" })

	assert.Equal(t, "HI!", execute(t, installedFuncs(), `{{ shout "hi" }}`))
	_, ok := before.Funcs(color.ProfilePlain, render.Roles{})["shout"]
	assert.True(t, !ok) // an operation that started earlier keeps its helpers
	_, ok = defaultIO.currentTheme().Editable().Funcs["shout"]
	assert.True(t, ok)

	WithFn("shout", func(s string) string { return s + "?" })
	assert.Equal(t, "hi?", execute(t, installedFuncs(), `{{ shout "hi" }}`))
}

func TestWithFnReachesWidgetTemplates(t *testing.T) {
	keepTheme(t)
	WithFn("shout", strings.ToUpper)
	funcs := installedFuncs()
	assert.Equal(t, "A", execute(t, funcs, `{{ shout "a" }}`))
	assert.Equal(t, "x", execute(t, funcs, `{{ label "x" }}`))
}

func TestWithFnPanicsAndKeepsInstalled(t *testing.T) {
	keepTheme(t)
	for name, fn := range map[string]any{
		"bold":                   strings.ToUpper,
		"fg":                     strings.ToUpper,
		"accent":                 strings.ToUpper,
		"symbol":                 strings.ToUpper,
		"label":                  strings.ToUpper,
		"ago":                    strings.ToUpper,
		"tableString":            strings.ToUpper,
		"__cellSep":              strings.ToUpper,
		"tableColumnTypeFormat0": strings.ToUpper,
		"1st":                    strings.ToUpper,
		"a-b":                    strings.ToUpper,
		"":                       strings.ToUpper,
		"notFunc":                "x",
		"nilFunc":                (func() string)(nil),
		"untyped":                nil,
		"noResult":               func() {},
		"badSecond":              func() (string, string) { return "", "" },
		"threeResults":           func() (int, int, error) { return 0, 0, nil },
	} {
		t.Run(name, func(t *testing.T) {
			installed := defaultIO.currentTheme()
			defer func() {
				r := recover()
				err, ok := r.(error)
				if !ok {
					t.Fatalf("want a panic with an error, got %v", r)
				}
				assert.Contains(t, err.Error(), "tui.WithFn: theme.Funcs."+name+":")
				assert.True(t, defaultIO.currentTheme() == installed)
			}()
			WithFn(name, fn)
		})
	}
}

func TestWithFnAcceptsErrorResult(t *testing.T) {
	keepTheme(t)
	boom := errors.New("boom")
	WithFn("fails", func() (string, error) { return "", boom })
	tmpl, err := template.New("t").Funcs(installedFuncs()).Parse(`{{ fails }}`)
	assert.NoError(t, err)
	assert.ErrorIs(t, tmpl.Execute(&strings.Builder{}, nil), boom)
}

func TestConcurrentWithFnLosesNothing(t *testing.T) {
	keepTheme(t)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() { WithFn(fmt.Sprintf("fn%d", i), strings.ToUpper) })
	}
	wg.Wait()
	funcs := defaultIO.currentTheme().Editable().Funcs
	for i := range 16 {
		_, ok := funcs[fmt.Sprintf("fn%d", i)]
		assert.True(t, ok)
	}
}

func TestInstallReplacesFuncs(t *testing.T) {
	keepTheme(t)
	WithFn("forgotten", strings.ToUpper)
	th := theme.New()
	th.Funcs = template.FuncMap{"kept": strings.ToLower}
	assert.NoError(t, defaultIO.installTheme(th))
	funcs := defaultIO.currentTheme().Editable().Funcs
	_, forgotten := funcs["forgotten"]
	_, kept := funcs["kept"]
	assert.True(t, !forgotten && kept)
}

func TestSetDefaultThemePublishesAndKeepsPreviousOnError(t *testing.T) {
	keepTheme(t)
	WithFn("shout", strings.ToUpper)
	th := DefaultTheme()
	// unset colors stay unset, so they keep adapting
	assert.Equal(t, theme.RGB(""), th.Palette.Dark.Foreground)
	_, ok := th.Funcs["shout"]
	assert.True(t, ok)

	th.Palette.Accent = "#0AD"
	th.Inline = true
	assert.NoError(t, SetDefaultTheme(th))
	th.Palette.Accent = "#FF0000" // later edits do not reach the installed copy
	installed := DefaultTheme()
	assert.Equal(t, theme.RGB("#00AADD"), installed.Palette.Accent)
	assert.True(t, installed.Inline)

	bad := DefaultTheme()
	bad.Palette.Error = "red"
	err := SetDefaultTheme(bad)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error")
	assert.Equal(t, theme.RGB("#00AADD"), DefaultTheme().Palette.Accent)
}

func TestRemovedColorNamesFailWithTemplatePath(t *testing.T) {
	th := theme.New()
	th.Templates.Prompt.Label = `{{ red .Label }}`
	err := SetDefaultTheme(th)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Prompt.Label")
	assert.Contains(t, err.Error(), `function "red" not defined`)
}

func TestThemeFuncsAreCopied(t *testing.T) {
	keepTheme(t)
	th := theme.New()
	th.Funcs = template.FuncMap{"h": strings.ToUpper}
	assert.NoError(t, defaultIO.installTheme(th))
	th.Funcs["h"] = strings.ToLower
	th.Funcs["extra"] = strings.ToLower
	got := defaultIO.currentTheme().Editable()
	got.Funcs["h"] = strings.ToLower
	assert.Equal(t, "A", execute(t, installedFuncs(), `{{ h "a" }}`))
	_, ok := defaultIO.currentTheme().Editable().Funcs["extra"]
	assert.True(t, !ok)
}

func TestSpinnerFramesAreCopied(t *testing.T) {
	keepTheme(t)
	th := theme.New()
	th.Symbols.Spinner = []string{"a", "b"}
	assert.NoError(t, defaultIO.installTheme(th))
	installed := defaultIO.currentTheme()
	th.Symbols.Spinner[0] = "x"
	got := installed.Editable()
	got.Symbols.Spinner[1] = "y"
	assert.Equal(t, []string{"a", "b"}, installed.Source.Symbols.Spinner)
	// the presets are not shared with editable themes
	theme.New().Symbols.Spinner[0] = "?"
	assert.Equal(t, "▘", theme.New().Symbols.Spinner[0])
	assert.Equal(t, "▘", theme.New().Symbols.Spinner[0])
}
