// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestJSONIndentFormats(t *testing.T) {
	type data struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	buf, err := jsonIndent(data{Name: "Alice", Age: 30})
	if err != nil {
		t.Fatalf("jsonIndent failed: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"name": "Alice"`)) {
		t.Fatalf("expected name field")
	}
}

func TestJSONIndentInvalidJSON(t *testing.T) {
	_, err := jsonIndent("{")
	if err == nil {
		t.Fatalf("expected error")
	}
}

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
	stack = new(jsonStyle).pretttJsonBackslash(stack, &out, '\\')
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
	_, stack = testJSONStyle().prettyJsonLoop(stack, &out, 'x', 0)
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
		buf, err := jsonIndent(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		var out bytes.Buffer
		testJSONStyle().recolor(buf, &out)
	}
}

func FuzzPrettyJsonRecolor(f *testing.F) {
	f.Add(`{"x":"a\"}}x"}`)
	f.Add(`[1,"\\",{"a":[]}]`)
	f.Fuzz(func(t *testing.T, in string) {
		buf, err := jsonIndent(in)
		if err != nil {
			t.Skip()
		}
		var out bytes.Buffer
		testJSONStyle().recolor(buf, &out)
	})
}

func testJSONStyle() *jsonStyle {
	st := jsonStyles[profileTrueColor]()
	return &st
}

const jsonSample = `{"a":{"b":[1,"x\"y",null,true,{"c":"\\"}]},"k{":"v}"}`

func renderJSON(t *testing.T, env string, src any) string {
	t.Helper()
	withEnv(t, env)
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
	pal := jsonPalette()
	assert.True(t, !pal.Known)
	assert.Equal(t, string(profileTrueColor.fg(pal.Accent))+bold, shadeBefore(t, out, `"key"`))
	// the background is unknown, so values keep the terminal default
	// foreground and brackets are bold rather than a fixed neutral
	for _, v := range []string{`"value"`, `-1.5`, `true`, `null`} {
		assert.Contains(t, out, ": "+v)
	}
	assert.Contains(t, out, bold+"{")
}

func TestJSONStyleDerivesFromResolvedRoles(t *testing.T) {
	resolve := func(p Palette) resolvedPalette {
		pal, err := p.resolve(false, sRGB{0x12, 0x12, 0x12}, true) // known, so neutrals are emitted
		assert.NoError(t, err)
		return pal
	}
	base := newJSONStyle(profileTrueColor, resolve(Palette{}))
	accent := newJSONStyle(profileTrueColor, resolve(Palette{Accent: "#2D9B55"}))
	assert.Equal(t, 5, len(base.keys))
	assert.True(t, !bytes.Equal(base.keys[0], accent.keys[0]))
	assert.Equal(t, string(base.value), string(accent.value))
	assert.Equal(t, string(base.punct), string(accent.punct))

	fg := newJSONStyle(profileTrueColor, resolve(Palette{Dark: PaletteVariant{Foreground: "#FFE0A0"}}))
	assert.True(t, !bytes.Equal(base.value, fg.value))
	assert.Equal(t, string(base.keys[0]), string(fg.keys[0]))
	assert.True(t, !bytes.Equal(base.punct, fg.punct)) // muted derives from foreground

	muted := newJSONStyle(profileTrueColor, resolve(Palette{Dark: PaletteVariant{Muted: "#888888"}}))
	assert.True(t, !bytes.Equal(base.punct, muted.punct))
	assert.Equal(t, string(base.value), string(muted.value))
}

// displayed decodes the foreground that a destination of profile p shows for sgr.
func displayed(t *testing.T, p colorProfile, sgr text) sRGB {
	t.Helper()
	var n, r, g, b int
	switch p {
	case profileTrueColor:
		_, err := fmt.Sscanf(string(sgr), "\x1b[38;2;%d;%d;%dm", &r, &g, &b)
		assert.NoError(t, err)
		return sRGB{uint8(r), uint8(g), uint8(b)}
	case profileANSI256:
		_, err := fmt.Sscanf(string(sgr), "\x1b[38;5;%dm", &n)
		assert.NoError(t, err)
		return ansiPalette()[n]
	default:
		_, err := fmt.Sscanf(string(sgr), "\x1b[%dm", &n)
		assert.NoError(t, err)
		if n >= 90 {
			return ansiPalette()[n-90+8]
		}
		return ansiPalette()[n-30]
	}
}

func TestJSONKeyShadesDimMonotonicallyOnEveryProfile(t *testing.T) {
	pal := jsonPalette()
	for _, p := range []colorProfile{profileANSI16, profileANSI256, profileTrueColor} {
		keys := newJSONStyle(p, pal).keys
		assert.True(t, len(keys) > 0)
		prev := 22.0
		for _, k := range keys {
			c := displayed(t, p, k).contrast(pal.Background)
			assert.True(t, c < prev)          // strictly dimmer, so also distinct
			assert.True(t, c >= contrastText) // keys are words
			prev = c
		}
	}
	// the truecolor ramp has every level
	assert.Equal(t, jsonLevels, len(newJSONStyle(profileTrueColor, pal).keys))
}

func TestJSONKeysAcrossLightAndDarkBackgrounds(t *testing.T) {
	for _, bg := range []sRGB{{0, 0, 0}, {0x12, 0x12, 0x12}, {0x77, 0x77, 0x77}, {0xEE, 0xEE, 0xEE}, {255, 255, 255}} {
		pal, err := Palette{}.resolve(false, bg, true)
		assert.NoError(t, err)
		for _, p := range []colorProfile{profileANSI16, profileANSI256, profileTrueColor} {
			prev := 22.0
			for _, k := range newJSONStyle(p, pal).keys {
				c := displayed(t, p, k).contrast(bg)
				assert.True(t, c < prev)
				assert.True(t, c >= contrastText)
				prev = c
			}
		}
	}
}

func TestPrettyJSONDeeperKeysAreDimmer(t *testing.T) {
	out := renderJSON(t, "FORCE_COLOR=3", `{"a":{"b":{"c":{"d":{"e":{"f":"s","g":-7}}}}}}`)
	bg := jsonPalette().Background
	prev := 22.0
	for _, k := range []string{`"a"`, `"b"`, `"c"`, `"d"`, `"e"`} {
		sgr := strings.TrimSuffix(shadeBefore(t, out, k), bold)
		c := displayed(t, profileTrueColor, text(sgr)).contrast(bg)
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
		assert.True(t, strings.HasSuffix(out, reset+"\n"))
		assert.Equal(t, renderJSON(t, "FORCE_COLOR=0", in), regexpSGR.ReplaceAllString(out, ""))
	}
}

func TestPrettyJSONAttrsOnlyHasNoColor(t *testing.T) {
	withEnv(t, "NO_COLOR=1")
	w := ttyWriter(t)
	assert.NoError(t, PrettyJSON(w, `{"a":1}`))
	out := w.Writer.(*bytes.Buffer).String() //nolint:errcheck,forcetypeassert // test writer
	assert.Contains(t, out, bold)
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

func TestJSONStyleWithoutColor(t *testing.T) {
	st := newJSONStyle(profileAttrs, jsonPalette())
	assert.Equal(t, text(bold), st.punct)
	assert.Equal(t, 0, len(st.keys))
	assert.Equal(t, 0, len(st.value))
}

func TestJSONStyleWithKnownBackgroundColorsNeutrals(t *testing.T) {
	pal, err := Palette{}.resolve(false, sRGB{0xFF, 0xFF, 0xFF}, true)
	assert.NoError(t, err)
	st := newJSONStyle(profileTrueColor, pal)
	assert.Equal(t, string(profileTrueColor.fg(pal.Foreground)), string(st.value))
	assert.Equal(t, string(profileTrueColor.fg(pal.Muted)), string(st.punct))
}
