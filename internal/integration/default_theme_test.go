// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"errors"
	"fmt"
	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"
	"strings"
	"sync"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestInstallAtomicAndIsolation(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())

	th := theme.New()
	th.Palette.Accent = "#112233"
	assert.NoError(t, term.Default.InstallTheme(th))
	c := term.Default.CurrentTheme()
	assert.Equal(t, theme.RGB("#112233"), c.Editable().Palette.Accent)

	// Mutating the caller's value and the returned copy leaves the snapshot alone.
	th.Palette.Accent = "#445566"
	got := term.Default.CurrentTheme().Editable()
	got.Palette.Accent = "#778899"
	assert.Equal(t, theme.RGB("#112233"), c.Source.Palette.Accent)
	assert.Equal(t, theme.RGB("#112233"), term.Default.CurrentTheme().Editable().Palette.Accent)

	// A failed installation changes nothing.
	bad := theme.New()
	bad.Palette.Accent = "nope"
	assert.Error(t, term.Default.InstallTheme(bad))
	assert.True(t, term.Default.CurrentTheme() == c)
}

func TestConcurrentInstall(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			th := theme.New()
			if i%2 == 0 {
				th.Palette.Accent = "nope"
			}
			err := term.Default.InstallTheme(th)
			if i%2 == 0 {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			if c := term.Default.CurrentTheme(); c == nil {
				t.Error("torn theme")
			}
		})
	}
	wg.Wait()
}

func TestWithFnRegistersForFutureOperations(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
	before := term.Default.CurrentTheme()
	tui.WithFn("shout", func(s string) string { return strings.ToUpper(s) + "!" })

	assert.Equal(t, "HI!", execute(t, installedFuncs(), `{{ shout "hi" }}`))
	_, ok := before.Funcs(color.ProfilePlain, render.Roles{})["shout"]
	assert.True(t, !ok) // an operation that started earlier keeps its helpers
	_, ok = term.Default.CurrentTheme().Editable().Funcs["shout"]
	assert.True(t, ok)

	tui.WithFn("shout", func(s string) string { return s + "?" })
	assert.Equal(t, "hi?", execute(t, installedFuncs(), `{{ shout "hi" }}`))
}

func TestWithFnReachesWidgetTemplates(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
	tui.WithFn("shout", strings.ToUpper)
	funcs := installedFuncs()
	assert.Equal(t, "A", execute(t, funcs, `{{ shout "a" }}`))
	assert.Equal(t, "x", execute(t, funcs, `{{ label "x" }}`))
}

func TestWithFnPanicsAndKeepsInstalled(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
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
			installed := term.Default.CurrentTheme()
			defer func() {
				r := recover()
				err, ok := r.(error)
				if !ok {
					t.Fatalf("want a panic with an error, got %v", r)
				}
				assert.Contains(t, err.Error(), "tui.WithFn: theme.Funcs."+name+":")
				assert.True(t, term.Default.CurrentTheme() == installed)
			}()
			tui.WithFn(name, fn)
		})
	}
}

func TestWithFnAcceptsErrorResult(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
	boom := errors.New("boom")
	tui.WithFn("fails", func() (string, error) { return "", boom })
	tmpl, err := template.New("t").Funcs(installedFuncs()).Parse(`{{ fails }}`)
	assert.NoError(t, err)
	assert.ErrorIs(t, tmpl.Execute(&strings.Builder{}, nil), boom)
}

func TestConcurrentWithFnLosesNothing(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() { tui.WithFn(fmt.Sprintf("fn%d", i), strings.ToUpper) })
	}
	wg.Wait()
	funcs := term.Default.CurrentTheme().Editable().Funcs
	for i := range 16 {
		_, ok := funcs[fmt.Sprintf("fn%d", i)]
		assert.True(t, ok)
	}
}

func TestInstallReplacesFuncs(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
	tui.WithFn("forgotten", strings.ToUpper)
	th := theme.New()
	th.Funcs = template.FuncMap{"kept": strings.ToLower}
	assert.NoError(t, term.Default.InstallTheme(th))
	funcs := term.Default.CurrentTheme().Editable().Funcs
	_, forgotten := funcs["forgotten"]
	_, kept := funcs["kept"]
	assert.True(t, !forgotten && kept)
}

func TestSetDefaultThemePublishesAndKeepsPreviousOnError(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
	tui.WithFn("shout", strings.ToUpper)
	th := tui.DefaultTheme()
	// unset colors stay unset, so they keep adapting
	assert.Equal(t, theme.RGB(""), th.Palette.Dark.Foreground)
	_, ok := th.Funcs["shout"]
	assert.True(t, ok)

	th.Palette.Accent = "#0AD"
	th.Inline = true
	assert.NoError(t, tui.SetDefaultTheme(th))
	th.Palette.Accent = "#FF0000" // later edits do not reach the installed copy
	installed := tui.DefaultTheme()
	assert.Equal(t, theme.RGB("#00AADD"), installed.Palette.Accent)
	assert.True(t, installed.Inline)

	bad := tui.DefaultTheme()
	bad.Palette.Error = "red"
	err := tui.SetDefaultTheme(bad)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error")
	assert.Equal(t, theme.RGB("#00AADD"), tui.DefaultTheme().Palette.Accent)
}

func TestRemovedColorNamesFailWithTemplatePath(t *testing.T) {
	th := theme.New()
	th.Prompt.Label = `{{ red .Label }}`
	err := tui.SetDefaultTheme(th)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Prompt.Label")
	assert.Contains(t, err.Error(), `function "red" not defined`)
}

func TestThemeFuncsAreCopied(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
	th := theme.New()
	th.Funcs = template.FuncMap{"h": strings.ToUpper}
	assert.NoError(t, term.Default.InstallTheme(th))
	th.Funcs["h"] = strings.ToLower
	th.Funcs["extra"] = strings.ToLower
	got := term.Default.CurrentTheme().Editable()
	got.Funcs["h"] = strings.ToLower
	assert.Equal(t, "A", execute(t, installedFuncs(), `{{ h "a" }}`))
	_, ok := term.Default.CurrentTheme().Editable().Funcs["extra"]
	assert.True(t, !ok)
}

func TestSpinnerFramesAreCopied(t *testing.T) {
	terst.UseTheme(t, tui.DefaultTheme())
	th := theme.New()
	th.Symbols.Spinner = []string{"a", "b"}
	assert.NoError(t, term.Default.InstallTheme(th))
	installed := term.Default.CurrentTheme()
	th.Symbols.Spinner[0] = "x"
	got := installed.Editable()
	got.Symbols.Spinner[1] = "y"
	assert.Equal(t, []string{"a", "b"}, installed.Source.Symbols.Spinner)
	// the presets are not shared with editable themes
	theme.New().Symbols.Spinner[0] = "?"
	assert.Equal(t, "▘", theme.New().Symbols.Spinner[0])
	assert.Equal(t, "▘", theme.New().Symbols.Spinner[0])
}

// installedFuncs are the helpers of the installed theme, for a plain destination.
func installedFuncs() template.FuncMap {
	return term.Default.CurrentTheme().Funcs(color.ProfilePlain, render.Roles{})
}

// execute renders src with funcs and fails the test on any error.
func execute(t *testing.T, funcs template.FuncMap, src string) string {
	t.Helper()
	tmpl, err := template.New("t").Funcs(funcs).Parse(src)
	assert.NoError(t, err)
	var b strings.Builder
	assert.NoError(t, tmpl.Execute(&b, nil))
	return b.String()
}
