// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package table

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/render"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestTableHeadersFollowProfile(t *testing.T) {
	type row struct{ Name string }
	for _, tc := range []struct {
		env  []string
		bold bool
	}{{nil, false}, {[]string{"FORCE_COLOR=1"}, true}} {
		terst.WithEnv(t, tc.env...)
		var out bytes.Buffer
		assert.NoError(t, TableAuto(&out, []row{{"a"}}))
		assert.Equal(t, tc.bold, strings.Contains(out.String(), "\x1b[1m"))
	}
}

func TestFactsFollowProfile(t *testing.T) {
	type row struct{ Name string }
	terst.WithEnv(t)
	var out bytes.Buffer
	assert.NoError(t, Facts(&out, row{"a"}))
	assert.NotContains(t, out.String(), "\x1b[")
	terst.WithEnv(t, "FORCE_COLOR=1")
	out.Reset()
	assert.NoError(t, Facts(&out, row{"a"}))
	assert.Contains(t, out.String(), "\x1b[1m")
}

func TestGeometryValueColumns(t *testing.T) {
	long := ansi.Text(strings.Repeat("x", 100))
	for _, tc := range []struct {
		name     string
		inline   bool
		guide    string
		width    int
		label    ansi.Text
		wantLead string
		wantCols int
		wantErr  bool
	}{
		{"guide unknown width", false, "│", 0, ansi.Text("Name: "), "│ ", math.MaxInt, false},
		{"inline unknown width", true, "│", 0, ansi.Text("Name: "), "Name: ", math.MaxInt, false},
		{"guide width 1", false, "│", 1, nil, "│ ", 0, true},
		{"guide width 2", false, "│", 2, nil, "│ ", 0, true},
		{"guide width 3 holds only the caret", false, "│", 3, nil, "│ ", 0, true},
		{"guide width 4", false, "│", 4, nil, "│ ", 1, false},
		{"guide label does not count", false, "│", 80, long, "│ ", 77, false},
		{"ascii guide", false, "|", 10, nil, "| ", 7, false},
		{"inline width 1", true, "│", 1, ansi.Text("Name: "), "", 0, true},
		{"inline width 2 drops the label", true, "│", 2, ansi.Text("Name: "), "", 1, false},
		{"inline label fits exactly", true, "│", 8, ansi.Text("Name: "), "Name: ", 1, false},
		{"inline long label is clipped", true, "│", 7, ansi.Text("Name: "), "Name…", 1, false},
		{"inline empty label", true, "│", 10, nil, "", 9, false},
		{"inline wide label", true, "│", 6, ansi.Text("\u754c\u9762\u754c: "), "\u754c…", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := render.Geometry{Inline: tc.inline, Guide: tc.guide, Width: tc.width, Height: 1}
			lead := g.ValueLead(tc.label)
			assert.Equal(t, tc.wantLead, lead.String())
			cols, err := g.ValueColumns(lead)
			if tc.wantErr {
				assert.ErrorIs(t, err, errs.ErrNoSpace)
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
		rows          []render.FrameRow
		want          want
		wantErr       bool
	}{
		{"unknown size keeps everything", false, 0, 0, []render.FrameRow{
			{Content: ansi.Text("label")},
			{Content: ansi.Text(strings.Repeat("v", 300)), Guided: true},
			{Content: ansi.Text("hint"), Guided: true, Drop: 1},
		}, want{[]string{"label", "│ " + strings.Repeat("v", 300), "│ hint"}, []int{0, 1, 2}}, false},
		{"height 1 keeps the required row", false, 20, 1, []render.FrameRow{
			{Content: ansi.Text("v")},
			{Content: ansi.Text("hint"), Guided: true, Drop: 1},
		}, want{[]string{"v"}, []int{0, -1}}, false},
		{"required rows exceed the height", false, 20, 1, []render.FrameRow{
			{Content: ansi.Text("a")}, {Content: ansi.Text("b")},
		}, want{}, true},
		{"largest drop goes first", false, 20, 3, []render.FrameRow{
			{Content: ansi.Text("value")},
			{Content: ansi.Text("details"), Drop: 1},
			{Content: ansi.Text("more"), Drop: 2},
			{Content: ansi.Text("hint"), Drop: 3},
		}, want{[]string{"value", "details", "more"}, []int{0, 1, 2, -1}}, false},
		{"short height drops more", false, 20, 2, []render.FrameRow{
			{Content: ansi.Text("value")},
			{Content: ansi.Text("details"), Drop: 1},
			{Content: ansi.Text("more"), Drop: 2},
			{Content: ansi.Text("hint"), Drop: 3},
		}, want{[]string{"value", "details"}, []int{0, 1, -1, -1}}, false},
		{"equal drop removes the later row", false, 20, 2, []render.FrameRow{
			{Content: ansi.Text("value")},
			{Content: ansi.Text("a"), Drop: 1},
			{Content: ansi.Text("b"), Drop: 1},
		}, want{[]string{"value", "a"}, []int{0, 1, -1}}, false},
		{"empty optional rows reserve nothing", false, 10, 0, []render.FrameRow{
			{Guided: true},
			{Drop: 1},
			{Content: ansi.Text("\x1b[2m\x1b[0m"), Drop: 1},
			{Content: ansi.Text("x")},
		}, want{[]string{"│ ", "x"}, []int{0, -1, -1, 1}}, false},
		{"exact fit is unchanged and one more is clipped", false, 10, 0, []render.FrameRow{
			{Content: ansi.Text("\x1b[1mabcdefg\x1b[0m"), Guided: true},
			{Content: ansi.Text("abcdefgh"), Guided: true},
		}, want{[]string{"│ \x1b[1mabcdefg\x1b[0m", "│ abcdef…"}, []int{0, 1}}, false},
		{"wide, combining and emoji clusters", false, 6, 0, []render.FrameRow{
			{Content: ansi.Text("\u754c\u9762\u754c")},
			{Content: ansi.Text(strings.Repeat("e\u0301", 5))},
			{Content: ansi.Text("👍👍👍")},
		}, want{[]string{"\u754c\u9762…", strings.Repeat("e\u0301", 5), "👍👍…"}, []int{0, 1, 2}}, false},
		{"inline has no guide", true, 10, 0, []render.FrameRow{
			{Content: ansi.Text("abc"), Guided: true},
		}, want{[]string{"abc"}, []int{0}}, false},
		{"width 2 cannot hold the guide", false, 2, 0, []render.FrameRow{
			{Guided: true},
		}, want{}, true},
		{"width 3 keeps an empty guided row", false, 3, 0, []render.FrameRow{
			{Guided: true},
			{Content: ansi.Text("hint"), Guided: true, Drop: 1},
		}, want{[]string{"│ "}, []int{0, -1}}, false},
		{"width 3 cannot show guided content", false, 3, 0, []render.FrameRow{
			{Content: ansi.Text("x"), Guided: true},
		}, want{}, true},
		{"width 1 cannot show content", true, 1, 0, []render.FrameRow{
			{Content: ansi.Text("x")},
		}, want{}, true},
		{"width 2 shows the ellipsis", true, 2, 0, []render.FrameRow{
			{Content: ansi.Text("xy")},
		}, want{[]string{"…"}, []int{0}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := render.Geometry{Inline: tc.inline, Guide: "│", Width: tc.width, Height: tc.height}
			got, err := g.Fit(tc.rows)
			if tc.wantErr {
				assert.ErrorIs(t, err, errs.ErrNoSpace)
				return
			}
			assert.NoError(t, err)
			lines := []string{}
			for _, l := range got.Lines {
				lines = append(lines, l.String())
				if tc.width > 0 {
					assert.True(t, l.Width() < tc.width)
				}
			}
			assert.Equal(t, tc.want.lines, lines)
			assert.Equal(t, tc.want.at, got.At)
		})
	}
}

func TestFitDoesNotAliasRows(t *testing.T) {
	content := ansi.Text("abc")
	g := render.Geometry{Guide: "│", Width: 10}
	got, err := g.Fit([]render.FrameRow{{Content: content, Guided: true}})
	assert.NoError(t, err)
	got.Lines[0][len(got.Lines[0])-1] = 'X'
	assert.Equal(t, "abc", content.String())
}
