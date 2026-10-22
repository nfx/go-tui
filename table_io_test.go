// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestTableHeadersFollowProfile(t *testing.T) {
	type row struct{ Name string }
	for _, tc := range []struct {
		env  []string
		bold bool
	}{{nil, false}, {[]string{"FORCE_COLOR=1"}, true}} {
		withEnv(t, tc.env...)
		var out bytes.Buffer
		assert.NoError(t, TableAuto(&out, []row{{"a"}}))
		assert.Equal(t, tc.bold, strings.Contains(out.String(), "\x1b[1m"))
	}
}

func TestFactsFollowProfile(t *testing.T) {
	type row struct{ Name string }
	withEnv(t)
	var out bytes.Buffer
	assert.NoError(t, Facts(&out, row{"a"}))
	assert.NotContains(t, out.String(), "\x1b[")
	withEnv(t, "FORCE_COLOR=1")
	out.Reset()
	assert.NoError(t, Facts(&out, row{"a"}))
	assert.Contains(t, out.String(), "\x1b[1m")
}

func TestPrettyJSONLevels(t *testing.T) {
	render := func(env string) string { return renderJSON(t, env, `{"a":{"b":"c"}}`) }
	assert.NotContains(t, render("FORCE_COLOR=0"), "\x1b[")
	attrs := render("FORCE_COLOR=1")
	assert.True(t, regexp.MustCompile("\x1b\\[(3|9)[0-7]m").MatchString(attrs))
	assert.NotContains(t, attrs, "38;5;")
	assert.Contains(t, render("FORCE_COLOR=2"), "38;5;")
	assert.Contains(t, render("FORCE_COLOR=3"), "38;2;")
	stripped := regexpSGR.ReplaceAllString(render("FORCE_COLOR=1"), "")
	assert.Equal(t, "{\n  \"a\": {\n    \"b\": \"c\"\n  }\n}\n", stripped)
}

func TestGeometryValueColumns(t *testing.T) {
	long := Text(strings.Repeat("x", 100))
	for _, tc := range []struct {
		name     string
		inline   bool
		guide    string
		width    int
		label    Text
		wantLead string
		wantCols int
		wantErr  bool
	}{
		{"guide unknown width", false, "│", 0, Text("Name: "), "│ ", math.MaxInt, false},
		{"inline unknown width", true, "│", 0, Text("Name: "), "Name: ", math.MaxInt, false},
		{"guide width 1", false, "│", 1, nil, "│ ", 0, true},
		{"guide width 2", false, "│", 2, nil, "│ ", 0, true},
		{"guide width 3 holds only the caret", false, "│", 3, nil, "│ ", 0, true},
		{"guide width 4", false, "│", 4, nil, "│ ", 1, false},
		{"guide label does not count", false, "│", 80, long, "│ ", 77, false},
		{"ascii guide", false, "|", 10, nil, "| ", 7, false},
		{"inline width 1", true, "│", 1, Text("Name: "), "", 0, true},
		{"inline width 2 drops the label", true, "│", 2, Text("Name: "), "", 1, false},
		{"inline label fits exactly", true, "│", 8, Text("Name: "), "Name: ", 1, false},
		{"inline long label is clipped", true, "│", 7, Text("Name: "), "Name…", 1, false},
		{"inline empty label", true, "│", 10, nil, "", 9, false},
		{"inline wide label", true, "│", 6, Text("\u754c\u9762\u754c: "), "\u754c…", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := geometry{inline: tc.inline, guide: tc.guide, width: tc.width, height: 1}
			lead := g.valueLead(tc.label)
			assert.Equal(t, tc.wantLead, lead.String())
			cols, err := g.valueColumns(lead)
			if tc.wantErr {
				assert.ErrorIs(t, err, ErrNoSpace)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantCols, cols)
			if tc.width > 0 {
				// the window and its caret cell fill the row exactly
				assert.Equal(t, tc.width, lead.Width()+cols+1)
			}
		})
	}
}

func TestGeometryFit(t *testing.T) {
	type want struct {
		lines []string
		at    []int
	}
	for _, tc := range []struct {
		name          string
		inline        bool
		width, height int
		rows          []frameRow
		want          want
		wantErr       bool
	}{
		{"unknown size keeps everything", false, 0, 0, []frameRow{
			{content: Text("label")},
			{content: Text(strings.Repeat("v", 300)), guided: true},
			{content: Text("hint"), guided: true, drop: 1},
		}, want{[]string{"label", "│ " + strings.Repeat("v", 300), "│ hint"}, []int{0, 1, 2}}, false},
		{"height 1 keeps the required row", false, 20, 1, []frameRow{
			{content: Text("v")},
			{content: Text("hint"), guided: true, drop: 1},
		}, want{[]string{"v"}, []int{0, -1}}, false},
		{"required rows exceed the height", false, 20, 1, []frameRow{
			{content: Text("a")}, {content: Text("b")},
		}, want{}, true},
		{"largest drop goes first", false, 20, 3, []frameRow{
			{content: Text("value")},
			{content: Text("details"), drop: 1},
			{content: Text("more"), drop: 2},
			{content: Text("hint"), drop: 3},
		}, want{[]string{"value", "details", "more"}, []int{0, 1, 2, -1}}, false},
		{"short height drops more", false, 20, 2, []frameRow{
			{content: Text("value")},
			{content: Text("details"), drop: 1},
			{content: Text("more"), drop: 2},
			{content: Text("hint"), drop: 3},
		}, want{[]string{"value", "details"}, []int{0, 1, -1, -1}}, false},
		{"equal drop removes the later row", false, 20, 2, []frameRow{
			{content: Text("value")},
			{content: Text("a"), drop: 1},
			{content: Text("b"), drop: 1},
		}, want{[]string{"value", "a"}, []int{0, 1, -1}}, false},
		{"empty optional rows reserve nothing", false, 10, 0, []frameRow{
			{guided: true},
			{drop: 1},
			{content: Text("\x1b[2m\x1b[0m"), drop: 1},
			{content: Text("x")},
		}, want{[]string{"│ ", "x"}, []int{0, -1, -1, 1}}, false},
		{"exact fit is unchanged and one more is clipped", false, 10, 0, []frameRow{
			{content: Text("\x1b[1mabcdefg\x1b[0m"), guided: true},
			{content: Text("abcdefgh"), guided: true},
		}, want{[]string{"│ \x1b[1mabcdefg\x1b[0m", "│ abcdef…"}, []int{0, 1}}, false},
		{"wide, combining and emoji clusters", false, 6, 0, []frameRow{
			{content: Text("\u754c\u9762\u754c")},
			{content: Text(strings.Repeat("e\u0301", 5))},
			{content: Text("👍👍👍")},
		}, want{[]string{"\u754c\u9762…", strings.Repeat("e\u0301", 5), "👍👍…"}, []int{0, 1, 2}}, false},
		{"inline has no guide", true, 10, 0, []frameRow{
			{content: Text("abc"), guided: true},
		}, want{[]string{"abc"}, []int{0}}, false},
		{"width 2 cannot hold the guide", false, 2, 0, []frameRow{
			{guided: true},
		}, want{}, true},
		{"width 3 keeps an empty guided row", false, 3, 0, []frameRow{
			{guided: true},
			{content: Text("hint"), guided: true, drop: 1},
		}, want{[]string{"│ "}, []int{0, -1}}, false},
		{"width 3 cannot show guided content", false, 3, 0, []frameRow{
			{content: Text("x"), guided: true},
		}, want{}, true},
		{"width 1 cannot show content", true, 1, 0, []frameRow{
			{content: Text("x")},
		}, want{}, true},
		{"width 2 shows the ellipsis", true, 2, 0, []frameRow{
			{content: Text("xy")},
		}, want{[]string{"…"}, []int{0}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := geometry{inline: tc.inline, guide: "│", width: tc.width, height: tc.height}
			got, err := g.fit(tc.rows)
			if tc.wantErr {
				assert.ErrorIs(t, err, ErrNoSpace)
				return
			}
			assert.NoError(t, err)
			lines := []string{}
			for _, l := range got.lines {
				lines = append(lines, l.String())
				if tc.width > 0 {
					assert.True(t, l.Width() < tc.width)
				}
			}
			assert.Equal(t, tc.want.lines, lines)
			assert.Equal(t, tc.want.at, got.at)
		})
	}
}

func TestFitDoesNotAliasRows(t *testing.T) {
	content := Text("abc")
	g := geometry{guide: "│", width: 10}
	got, err := g.fit([]frameRow{{content: content, guided: true}})
	assert.NoError(t, err)
	got.lines[0][len(got.lines[0])-1] = 'X'
	assert.Equal(t, "abc", content.String())
}
