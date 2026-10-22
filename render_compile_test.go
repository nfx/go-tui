// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/assert"
)

func TestSpanHelperHandlesTypes(t *testing.T) {
	str := "hi"
	ptr := &str
	arr := []string{"alpha", "beta"}
	num := 5
	formatter := destination{}.newSpan(reset, text("\x1b[31m"), text("\x1b[1m")).helper()

	got := formatter(ptr, arr, &num, "end")
	if !strings.HasPrefix(got, "\x1b[31m\x1b[1m") {
		t.Fatalf("expected prefix codes, got %q", got)
	}
	if !strings.Contains(got, "alpha, beta") {
		t.Fatalf("expected joined array, got %q", got)
	}
	if !strings.Contains(got, "5") {
		t.Fatalf("expected pointer value, got %q", got)
	}
	if !strings.HasSuffix(got, reset) {
		t.Fatalf("expected reset suffix, got %q", got)
	}
}

// execute renders src with funcs and fails the test on any error.
func execute(t *testing.T, funcs template.FuncMap, src string, data any) string {
	t.Helper()
	tmpl, err := template.New("t").Funcs(funcs).Parse(src)
	assert.NoError(t, err)
	var b strings.Builder
	assert.NoError(t, tmpl.Execute(&b, data))
	return b.String()
}

// installedFuncs are the helpers of the installed theme, for a plain destination.
func installedFuncs() template.FuncMap {
	return defaultIO.currentTheme().funcs(profilePlain, roleStyles{})
}

func TestThemeFuncsAreCopied(t *testing.T) {
	keepTheme(t)
	th := NewTheme()
	th.Funcs = template.FuncMap{"h": strings.ToUpper}
	assert.NoError(t, defaultIO.installTheme(th))
	th.Funcs["h"] = strings.ToLower
	th.Funcs["extra"] = strings.ToLower
	got := defaultIO.currentTheme().editable()
	got.Funcs["h"] = strings.ToLower
	assert.Equal(t, "A", execute(t, installedFuncs(), `{{ h "a" }}`, nil))
	_, ok := defaultIO.currentTheme().editable().Funcs["extra"]
	assert.True(t, !ok)
}

func TestThemeFuncsReachSlotsAndAreValidated(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Funcs = template.FuncMap{"shout": strings.ToUpper}
		th.Templates.Prompt.Label = `{{ shout .Label }}`
	})
	assert.NoError(t, err)
	b, err := c.templates.Input.Label.bind(c.funcs(profilePlain, roleStyles{}))
	assert.NoError(t, err)
	out, err := b.render(struct{ Label string }{"name"})
	assert.NoError(t, err)
	assert.Equal(t, "NAME", out.String())

	_, err = compileTemplates(t, func(th *Theme) {
		th.Funcs = template.FuncMap{"bold": strings.ToUpper, "ok": strings.ToUpper, "bad": 1}
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Funcs.bold: reserved")
	assert.Contains(t, err.Error(), "theme.Funcs.bad: want a function")
	assert.NotContains(t, err.Error(), "theme.Funcs.ok")
}

// truecolor supplies test helpers with truecolor output and an accent role.
func truecolor() template.FuncMap {
	return destination{
		profile: profileTrueColor,
		roles:   roleStyles{accent: text("\x1b[38;2;0;173;216m")},
		symbols: unicodeSymbols,
	}.funcs()
}

func TestStandaloneAttributeHelpersAreByteIdentical(t *testing.T) {
	funcs := truecolor()
	for src, want := range map[string]string{
		`{{ underline "x" }}`:                 "\x1b[4mx\x1b[0m",
		`{{ bold "x" 1 }}`:                    "\x1b[1mx1\x1b[0m",
		`{{ underline (bold "x") }}`:          "\x1b[4m\x1b[1mx\x1b[0m\x1b[0m",
		`{{ underline "" }}`:                  "\x1b[4m\x1b[0m",
		`{{ strike .S }}`:                     "\x1b[9ma, b\x1b[0m",
		`{{ underline (printf "%d" 5) }}`:     "\x1b[4m5\x1b[0m",
		`{{ dim "a\x1b[2Kb" }}`:               "\x1b[2ma\x1b[2Kb\x1b[0m", // cursor control is not a style
		`{{ underline "\x1b[38;5;0mx" }}`:     "\x1b[4m\x1b[38;5;0mx\x1b[0m",
		`{{ underline "\x1b[48;2;0;0;0mx" }}`: "\x1b[4m\x1b[48;2;0;0;0mx\x1b[0m",
	} {
		assert.Equal(t, want, execute(t, funcs, src, struct{ S []string }{[]string{"a", "b"}}))
	}
}

func TestNestedHelpersRestoreOuterStyle(t *testing.T) {
	funcs := truecolor()
	const accent = "\x1b[38;2;0;173;216m"
	for _, tc := range []struct{ name, src, want string }{
		{"attribute in attribute", `{{ underline (bold "x") " y" }}`, "\x1b[4m\x1b[1mx\x1b[0m\x1b[4m y\x1b[0m"},
		{"fg in bg", `{{ bg "#000" (fg "#FFF" "a") "b" }}`,
			"\x1b[48;2;0;0;0m\x1b[38;2;255;255;255ma\x1b[39mb\x1b[49m"},
		{"attribute in fg", `{{ fg "#F00" (underline "a") "b" }}`,
			"\x1b[38;2;255;0;0m\x1b[4ma\x1b[0m\x1b[38;2;255;0;0mb\x1b[39m"},
		{"fg in fg", `{{ fg "#F00" "a" (fg "#0F0" "b") "c" }}`,
			"\x1b[38;2;255;0;0ma\x1b[38;2;0;255;0mb\x1b[39m\x1b[38;2;255;0;0mc\x1b[39m"},
		{"attribute in semantic", `{{ accent (bold "a") "b" }}`, accent + "\x1b[1ma\x1b[0m" + accent + "b\x1b[39m"},
		{"semantic in attribute", `{{ bold (accent "a") "b" }}`, "\x1b[1m" + accent + "a\x1b[39mb\x1b[0m"},
		{"bold and dim share 22", `{{ dim "\x1b[1mx\x1b[22my" }}`, "\x1b[2m\x1b[1mx\x1b[22m\x1b[2my\x1b[0m"},
		{"selective reset of another channel", `{{ bold "\x1b[39mx" }}`, "\x1b[1m\x1b[39mx\x1b[0m"},
		{"three levels", `{{ italic (bold (underline "a") "b") "c" }}`,
			"\x1b[3m\x1b[1m\x1b[4ma\x1b[0m\x1b[3m\x1b[1mb\x1b[0m\x1b[3mc\x1b[0m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, execute(t, funcs, tc.src, nil))
		})
	}
}

func TestHelpersFollowProfile(t *testing.T) {
	attrs := destination{profile: profileAttrs}.funcs()
	plain := destination{profile: profilePlain}.funcs()
	for _, tc := range []struct {
		funcs     template.FuncMap
		src, want string
	}{
		{attrs, `{{ bold "x" }}`, "\x1b[1mx\x1b[0m"},
		{attrs, `{{ fg "#F00" "x" }}`, "x"},
		{attrs, `{{ accent "x" }}`, "x"},
		{plain, `{{ bold (accent "x") }}`, "x"},
		{plain, `{{ bg "#000" "x" }}`, "x"},
	} {
		assert.Equal(t, tc.want, execute(t, tc.funcs, tc.src, nil))
	}
	ansi16 := destination{profile: profileANSI16}.funcs()
	assert.Equal(t, "\x1b[91mx\x1b[39m", execute(t, ansi16, `{{ fg "#F00" "x" }}`, nil))
}

func TestColorHelpersComposeWithStringHelpers(t *testing.T) {
	up := template.FuncMap{"upper": func(s string) string { return "<" + s + ">" }}
	for _, tc := range []struct {
		profile   colorProfile
		src, want string
	}{
		{profileANSI16, `{{ fg "#F00" "hi" | upper }}`, "<\x1b[91mhi\x1b[39m>"},
		{profileANSI16, `{{ bg "#F00" "hi" | upper }}`, "<\x1b[101mhi\x1b[49m>"},
		{profilePlain, `{{ fg "#F00" "hi" | upper }}`, "<hi>"},
		{profilePlain, `{{ bg "#F00" "hi" | upper }}`, "<hi>"},
	} {
		funcs := destination{profile: tc.profile}.funcs()
		for k, v := range up {
			funcs[k] = v
		}
		assert.Equal(t, tc.want, execute(t, funcs, tc.src, nil))
	}
}

func TestColorHelperRejectsMalformedAtExecution(t *testing.T) {
	for _, src := range []string{`{{ fg "red" "x" }}`, `{{ bg "" "x" }}`, `{{ fg .C "x" }}`} {
		tmpl, err := template.New("t").Funcs(truecolor()).Parse(src)
		assert.NoError(t, err)
		err = tmpl.Execute(&strings.Builder{}, struct{ C string }{"#12"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "want \"#RRGGBB\"")
	}
}

func TestSymbolHelper(t *testing.T) {
	funcs := destination{symbols: asciiSymbols}.funcs()
	assert.Equal(t, ">", execute(t, funcs, `{{ symbol "Focus" }}`, nil))
	tmpl, err := template.New("t").Funcs(funcs).Parse(`{{ symbol "Nope" }}`)
	assert.NoError(t, err)
	assert.Error(t, tmpl.Execute(&strings.Builder{}, nil))
}

func TestClippingNestedHelpersKeepsStyleBalanced(t *testing.T) {
	out := text(execute(t, truecolor(), `{{ italic (bold "ab") "cd" }}`, nil)).truncateColumns(3)
	assert.Equal(t, 3, out.width())
	assert.Equal(t, "\x1b[3m\x1b[1mab\x1b[0m\x1b[3m…\x1b[0m", out.String())
}

func TestConcurrentHelperExecutions(t *testing.T) {
	tmpl, err := template.New("t").Funcs(truecolor()).Parse(`{{ italic (bold .) " y" }}`)
	assert.NoError(t, err)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			var b strings.Builder
			if err := tmpl.Execute(&b, i); err != nil {
				t.Error(err)
				return
			}
			want := fmt.Sprintf("\x1b[3m\x1b[1m%d\x1b[0m\x1b[3m y\x1b[0m", i)
			if b.String() != want {
				t.Errorf("got %q, want %q", b.String(), want)
			}
		})
	}
	wg.Wait()
}

func TestBuiltinHintAccentsKeys(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Symbols = asciiSymbols })
	assert.NoError(t, err)
	funcs := c.funcs(profileTrueColor, roleStyles{
		foreground: text("\x1b[38;5;1m"),
		accent:     text("\x1b[38;5;2m"),
		guide:      text("\x1b[38;5;3m"),
	})
	b, err := c.templates.Dropdown.Hint.bind(funcs)
	assert.NoError(t, err)
	out, err := b.render(hintView{{text("3"), text("more")}, {text("type"), text("filter")}})
	assert.NoError(t, err)
	// the left side is accented, the rest is the faintest
	assert.Equal(t, "\x1b[38;5;2m3\x1b[39m \x1b[38;5;3mmore\x1b[39m\x1b[38;5;3m · \x1b[39m"+
		"\x1b[38;5;2mtype\x1b[39m \x1b[38;5;3mfilter\x1b[39m", out.String())
}

func TestBuiltinHintSidesDifferOnAssumedBackground(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Symbols = asciiSymbols })
	assert.NoError(t, err)
	look := c.bind(profileTrueColor, background{})
	b, err := c.templates.Dropdown.Hint.bind(look.funcs)
	assert.NoError(t, err)
	out, err := b.render(hintView{{text("^v"), text("move")}})
	assert.NoError(t, err)
	// the neutrals keep the default color, so the right side is faint
	assert.Equal(t, "\x1b[38;2;0;173;216m^v\x1b[39m \x1b[2mmove\x1b[22m", out.String())
}
