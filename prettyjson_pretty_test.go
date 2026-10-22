// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/theme"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestPrettyJSONColor(t *testing.T) {
	withEnv(t, "FORCE_COLOR=2")

	var out bytes.Buffer
	err := PrettyJSON(&out, `{"hello":["world", "again"], "path":"a\\b"}`)
	if err != nil {
		t.Fatalf("PrettyJSON failed: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("\x1b[")) {
		t.Fatalf("expected color escape codes")
	}
}

func TestPrettyJSONNoColor(t *testing.T) {
	withEnv(t)

	var out bytes.Buffer
	err := PrettyJSON(&out, `{"flat":true}`)
	if err != nil {
		t.Fatalf("PrettyJSON failed: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"flat": true`)) {
		t.Fatalf("expected formatted JSON")
	}
}

func TestPrettyJsonBackslash(t *testing.T) {
	stack := []jsonState{jsonQuoted}
	var out bytes.Buffer
	stack = new(jsonLook).pretttJsonBackslash(stack, &out, '\\')
	if len(stack) != 2 {
		t.Fatalf("expected escape state")
	}
	if out.String() != "\\" {
		t.Fatalf("expected backslash output")
	}
}

func TestPrettyJsonLoopEscape(t *testing.T) {
	stack := []jsonState{jsonQuoted, jsonEscape}
	var out bytes.Buffer
	_, stack = testJSONLook().prettyJsonLoop(stack, &out, 'x', 0)
	if out.String() != "x" {
		t.Fatalf("expected escaped output")
	}
	if len(stack) != 1 || stack[0] != jsonQuoted {
		t.Fatalf("unexpected stack")
	}
}

func TestPrettyJsonRecolorCorpus(t *testing.T) {
	corpus := []string{
		`{"x":"a\"}}x"}`,
		`{"x":"a\\"}`,
		`{"x":"a\\\"}"}`,
		`{"k{":"v}","a]":["}",{"b":"\\"}]}`,
		`"root{"`,
		`"a\"}"`,
		`42`,
		`null`,
		`[]`,
		`[[1,[2,[]]],[{}],"]"]`,
		`{"a":{"b":{"c":1}},"d":[1,2,{"e":"\"}"}],"f":2}`,
		`{"a":1}`,
	}
	for _, in := range corpus {
		buf, err := jsonIndent(in, theme.New().JSON)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		var out bytes.Buffer
		testJSONLook().recolor(buf, &out)
	}
}

func FuzzPrettyJsonRecolor(f *testing.F) {
	f.Add(`{"x":"a\"}}x"}`)
	f.Add(`[1,"\\",{"a":[]}]`)
	f.Fuzz(func(t *testing.T, in string) {
		buf, err := jsonIndent(in, theme.New().JSON)
		if err != nil {
			t.Skip()
		}
		var out bytes.Buffer
		testJSONLook().recolor(buf, &out)
	})
}

// presetJSONTheme is the compiled preset theme.
var presetJSONTheme = sync.OnceValue(func() *render.Compiled {
	c, err := render.Compile(theme.New())
	if err != nil {
		panic(err) // the preset is valid
	}
	return c
})

func testJSONLook() *jsonLook {
	st := newJSONLook(presetJSONTheme().Bind(color.ProfileTrueColor, color.Background{}))
	return &st
}

// jsonBound binds th to a destination of profile p over bg, as PrettyJSON does.
func jsonBound(t *testing.T, th theme.Theme, p color.Profile, bg color.Background) render.Binding {
	t.Helper()
	c, err := render.Compile(th)
	assert.NoError(t, err)
	return c.Bind(p, bg)
}

// legacyJSONPalette is the palette of the preset on an unknown background.
func legacyJSONPalette() render.ResolvedPalette {
	return presetJSONTheme().Bind(color.ProfileTrueColor, color.Background{}).Palette
}

// measuredBG is a measured background, so neutral roles are emitted.
func measuredBG(rgb color.SRGB) color.Background {
	return color.Background{RGB: rgb, Light: rgb.IsLight(), Measured: true}
}

const jsonSample = `{"a":{"b":[1,"x\"y",null,true,{"c":"\\"}]},"k{":"v}"}`

// renderJSON renders src to a buffer in env, space-separated pairs such as
// FORCE_COLOR=3.
func renderJSON(t *testing.T, env string, src any) string {
	t.Helper()
	withEnv(t, strings.Fields(env)...)
	var out bytes.Buffer
	assert.NoError(t, PrettyJSON(&out, src))
	return out.String()
}

func TestPrettyJSONStrippedColorIsEquivalent(t *testing.T) {
	plain := renderJSON(t, "FORCE_COLOR=0", jsonSample)
	for _, env := range []string{"FORCE_COLOR=1", "FORCE_COLOR=2", "FORCE_COLOR=3", "CLICOLOR_FORCE=1"} {
		colored := renderJSON(t, env, jsonSample)
		assert.True(t, colored != plain)
		assert.Equal(t, plain, regexpSGR.ReplaceAllString(colored, ""))
	}
	assert.True(t, json.Valid([]byte(plain)))
}

// shadeBefore returns the truecolor SGR right before the first token.
func shadeBefore(t *testing.T, out, token string) string {
	t.Helper()
	i := strings.Index(out, token)
	assert.True(t, i > 0)
	m := regexp.MustCompile(`\x1b\[38;2;\d+;\d+;\d+m(\x1b\[1m)?$`).FindString(out[:i])
	assert.True(t, m != "")
	return m
}

func TestPrettyJSONRolesFollowPalette(t *testing.T) {
	out := renderJSON(t, "FORCE_COLOR=3", `{"key":"value","n":-1.5,"t":true,"z":null}`)
	pal := legacyJSONPalette()
	assert.True(t, !pal.Known)
	assert.Equal(t, string(color.ProfileTrueColor.Fg(pal.Accent))+ansi.Bold, shadeBefore(t, out, `"key"`))
	// the background is unknown, so values keep the terminal default
	// foreground and brackets are bold rather than a fixed neutral
	for _, v := range []string{`"value"`, `-1.5`, `true`, `null`} {
		assert.Contains(t, out, ": "+v)
	}
	assert.Contains(t, out, ansi.Bold+"{")
}

// displayed decodes the foreground that a destination of profile p shows for sgr.
func displayed(t *testing.T, p color.Profile, sgr ansi.Text) color.SRGB {
	t.Helper()
	var n, r, g, b int
	switch p {
	case color.ProfileTrueColor:
		_, err := fmt.Sscanf(string(sgr), "\x1b[38;2;%d;%d;%dm", &r, &g, &b)
		assert.NoError(t, err)
		return color.SRGB{uint8(r), uint8(g), uint8(b)}
	case color.ProfileANSI256:
		_, err := fmt.Sscanf(string(sgr), "\x1b[38;5;%dm", &n)
		assert.NoError(t, err)
		return color.ANSIPalette()[n]
	default:
		_, err := fmt.Sscanf(string(sgr), "\x1b[%dm", &n)
		assert.NoError(t, err)
		if n >= 90 {
			return color.ANSIPalette()[n-90+8]
		}
		return color.ANSIPalette()[n-30]
	}
}

func TestPrettyJSONDeeperKeysAreDimmer(t *testing.T) {
	out := renderJSON(t, "FORCE_COLOR=3", `{"a":{"b":{"c":{"d":{"e":{"f":"s","g":-7}}}}}}`)
	bg := legacyJSONPalette().Background
	prev := 22.0
	for _, k := range []string{`"a"`, `"b"`, `"c"`, `"d"`, `"e"`} {
		sgr := strings.TrimSuffix(shadeBefore(t, out, k), ansi.Bold)
		c := displayed(t, color.ProfileTrueColor, ansi.Text(sgr)).Contrast(bg)
		assert.True(t, c < prev)
		prev = c
	}
	// past the last level, the dimmest shade repeats
	assert.Equal(t, shadeBefore(t, out, `"e"`), shadeBefore(t, out, `"f"`))
	// values do not dim with depth
	assert.Contains(t, out, `: "s"`)
	assert.Contains(t, out, `: -7`)
	top := renderJSON(t, "FORCE_COLOR=3", `{"f":"s"}`)
	assert.Contains(t, top, `: "s"`)
}

func TestPrettyJSONScalarsAtRootAndInArrays(t *testing.T) {
	for _, in := range []string{`42`, `true`, `"s"`, `[1,false,"x",[null]]`} {
		out := renderJSON(t, "FORCE_COLOR=3", in)
		assert.True(t, strings.HasSuffix(out, ansi.Reset+"\n"))
		assert.Equal(t, renderJSON(t, "FORCE_COLOR=0", in), regexpSGR.ReplaceAllString(out, ""))
	}
}

func TestPrettyJSONAttrsOnlyHasNoColor(t *testing.T) {
	withEnv(t, "NO_COLOR=1")
	w := ttyWriter(t)
	assert.NoError(t, PrettyJSON(w, `{"a":1}`))
	out := w.Writer.(*bytes.Buffer).String() //nolint:errcheck,forcetypeassert // test writer
	assert.Contains(t, out, ansi.Bold)
	assert.NotContains(t, out, "\x1b[38")
}

func TestPrettyJSONPlainOnUnforcedBuffer(t *testing.T) {
	out := renderJSON(t, "X=1", `{"a":1}`)
	assert.Equal(t, "{\n  \"a\": 1\n}\n", out)
}

func TestPrettyJSONMalformedWritesNothing(t *testing.T) {
	withEnv(t, "FORCE_COLOR=3")
	var out bytes.Buffer
	assert.Error(t, PrettyJSON(&out, `{"a":`))
	assert.Equal(t, 0, out.Len())
}

// themedJSON renders src through th, installed with the theme gate on, to a
// buffer in the color environment env.
func themedJSON(t *testing.T, th theme.Theme, env string, src any) string {
	t.Helper()
	useTheme(t, th)
	return renderJSON(t, env, src)
}

func TestThemedPrettyJSONIndentation(t *testing.T) {
	src := `{"a":[1,{"b":"x y"}],"c":{},"d":[]}`
	four := &bytes.Buffer{}
	assert.NoError(t, json.Indent(four, []byte(src), "", "    "))
	for _, tc := range []struct {
		style theme.JSONStyle
		want  string
	}{
		{theme.JSONStyle{}, "{\n  \"a\": [\n    1,\n    {\n      \"b\": \"x y\"\n    }\n  ],\n  \"c\": {},\n  \"d\": []\n}\n"},
		{theme.JSONStyle{Indent: "\t"}, "{\n\t\"a\": [\n\t\t1,\n\t\t{\n\t\t\t\"b\": \"x y\"\n\t\t}\n\t],\n\t\"c\": {},\n\t\"d\": []\n}\n"},
		{theme.JSONStyle{Indent: "    "}, four.String() + "\n"},
		{theme.JSONStyle{Indent: "\t", Compact: true}, `{"a":[1,{"b":"x y"}],"c":{},"d":[]}` + "\n"},
	} {
		th := theme.New()
		th.JSON = tc.style
		plain := themedJSON(t, th, "X=1", src)
		assert.Equal(t, tc.want, plain)
		for _, env := range []string{"FORCE_COLOR=1", "FORCE_COLOR=3"} {
			colored := themedJSON(t, th, env, src)
			assert.True(t, colored != plain)
			assert.Equal(t, plain, regexpSGR.ReplaceAllString(colored, ""))
		}
		assert.True(t, json.Valid([]byte(plain)))
	}
	// a compact root scalar still ends its color before the newline
	th := theme.New()
	th.JSON.Compact = true
	assert.True(
		t,
		strings.HasSuffix(themedJSON(t, th, "FORCE_COLOR=3", `[1, 2]`), "2"+ansi.Reset+ansi.Bold+"]"+ansi.Reset+"\n"),
	)
	assert.True(t, strings.HasSuffix(themedJSON(t, th, "FORCE_COLOR=3", ` 42 `), "42"+ansi.Reset+"\n"))
}

func TestThemedPrettyJSONPresetMatchesLegacy(t *testing.T) {
	for _, env := range []string{"X=1", "FORCE_COLOR=1", "FORCE_COLOR=2", "FORCE_COLOR=3"} {
		legacy := renderJSON(t, env, jsonSample)
		assert.Equal(t, legacy, themedJSON(t, theme.New(), env, jsonSample))
	}
}

func TestThemedPrettyJSONFollowsInstalledPalette(t *testing.T) {
	src := `{"key":{"inner":1}}`
	var tops []string
	for _, accent := range []theme.RGB{"#2D9B55", "#D05AE0"} {
		th := theme.New()
		th.Palette.Accent = accent
		out := themedJSON(t, th, "FORCE_COLOR=3", src)
		look := jsonBound(t, th, color.ProfileTrueColor, color.Background{})
		keys := newJSONLook(look).keys
		assert.Equal(t, string(look.Roles.Accent)+ansi.Bold, shadeBefore(t, out, `"key"`))
		assert.Equal(t, string(keys[1])+ansi.Bold, shadeBefore(t, out, `"inner"`))
		tops = append(tops, shadeBefore(t, out, `"key"`))
	}
	// the same profile renders each installed palette: nothing is cached by profile
	assert.True(t, tops[0] != tops[1])
}

func TestThemedPrettyJSONExplicitAccentStaysLiteral(t *testing.T) {
	th := theme.New()
	th.Palette.Dark.Accent = "#3A3A3A" // below the text contrast on the dark reference
	out := themedJSON(t, th, "FORCE_COLOR=3", `{"a":{"b":1}}`)
	assert.Equal(t, "\x1b[38;2;58;58;58m"+ansi.Bold, shadeBefore(t, out, `"a"`))
	for _, p := range []color.Profile{color.ProfileANSI16, color.ProfileANSI256, color.ProfileTrueColor} {
		look := jsonBound(t, th, p, color.Background{})
		keys := newJSONLook(look).keys
		assert.Equal(t, string(look.Roles.Accent), string(keys[0]))
		prev := displayed(t, p, keys[0]).Contrast(look.Palette.Background)
		for _, k := range keys[1:] {
			c := displayed(t, p, k).Contrast(look.Palette.Background)
			assert.True(t, c < prev)
			assert.True(t, c >= color.ContrastText)
			prev = c
		}
	}
	// a readable explicit accent keeps every level, starting from the literal
	th.Palette.Dark.Accent = "#FFD75F"
	keys := newJSONLook(jsonBound(t, th, color.ProfileTrueColor, color.Background{})).keys
	assert.Equal(t, jsonLevels, len(keys))
	assert.Equal(t, "\x1b[38;2;255;215;95m", string(keys[0]))
}

func TestThemedPrettyJSONNeutralPolicy(t *testing.T) {
	src := `{"k":"v"}`
	// unknown background: values keep the terminal default, brackets are bold
	out := themedJSON(t, theme.New(), "FORCE_COLOR=3", src)
	assert.Contains(t, out, `: "v"`)
	assert.Contains(t, out, ansi.Bold+"{")

	// an estimate from COLORFGBG selects the light variant without measuring
	out = themedJSON(t, theme.New(), "FORCE_COLOR=3 COLORFGBG=0;15", src)
	light := jsonBound(t, theme.New(), color.ProfileTrueColor, color.Background{Light: true})
	dark := jsonBound(t, theme.New(), color.ProfileTrueColor, color.Background{})
	assert.True(t, string(light.Roles.Accent) != string(dark.Roles.Accent))
	assert.Equal(t, string(light.Roles.Accent)+ansi.Bold, shadeBefore(t, out, `"k"`))
	assert.Contains(t, out, `: "v"`)

	// forcing the light variant opts the neutrals in
	th := theme.New()
	th.Light = true
	out = themedJSON(t, th, "FORCE_COLOR=3", src)
	look := jsonBound(t, th, color.ProfileTrueColor, color.Background{})
	assert.True(t, len(look.Roles.Foreground) > 0 && len(look.Roles.Muted) > 0)
	assert.Contains(t, out, ": "+string(look.Roles.Foreground)+`"v"`)
	assert.Contains(t, out, string(look.Roles.Muted)+"{")

	// so do explicit neutrals, literally
	th = theme.New()
	th.Palette.Dark.Foreground = "#FFE0A0"
	th.Palette.Dark.Muted = "#888888"
	out = themedJSON(t, th, "FORCE_COLOR=3", src)
	assert.Contains(t, out, ": \x1b[38;2;255;224;160m\"v\"")
	assert.Contains(t, out, "\x1b[38;2;136;136;136m{")
}

func TestThemedPrettyJSONDestinations(t *testing.T) {
	th := theme.New()
	th.JSON.Indent = "\t"
	useTheme(t, th)

	// attributes only on a NO_COLOR terminal
	withEnv(t, "NO_COLOR=1")
	w := ttyWriter(t)
	assert.NoError(t, PrettyJSON(w, `{"a":1}`))
	out := w.Writer.(*bytes.Buffer).String() //nolint:errcheck,forcetypeassert // test writer
	assert.Equal(
		t,
		ansi.Bold+"{"+ansi.Reset+"\n\t"+ansi.Bold+`"a"`+ansi.Reset+": 1"+ansi.Reset+"\n"+ansi.Bold+"}"+ansi.Reset+"\n",
		out,
	)

	// write errors are returned
	withEnv(t, "FORCE_COLOR=3")
	assert.ErrorIs(t, PrettyJSON(errWriterTable{}, `{"a":1}`), io.EOF)

	// malformed input and marshal errors write nothing
	var buf bytes.Buffer
	assert.Error(t, PrettyJSON(&buf, `{"a":`))
	assert.Error(t, PrettyJSON(&buf, map[string]any{"f": func() {}}))
	assert.Equal(t, 0, buf.Len())
}
