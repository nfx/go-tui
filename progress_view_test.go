// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestThemedProgressRowDropsMetricsToFit(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	bar := func(fill, head, empty int) string {
		return strings.Repeat("=", fill) + strings.Repeat(">", head) + strings.Repeat("-", empty)
	}
	const all, two, one = " 50.00/s · 1s remaining · 1s elapsed", " 50.00/s · 1s remaining", " 50.00/s"
	for _, tc := range []struct {
		width int
		want  string
	}{
		{60, "build 50%  " + bar(6, 1, 6) + all},
		{48, "build 50%  " + bar(0, 1, 0) + all},
		{47, "build 50%  " + bar(6, 1, 6) + two},
		{35, "build 50%  " + bar(0, 1, 0) + two},
		{34, "build 50%  " + bar(7, 1, 7) + one},
		{20, "build 50%  " + bar(0, 1, 0) + one},
		{19, "build 50%  " + bar(4, 0, 4)},
		{12, "build 50%  " + bar(0, 1, 0)},
		{11, "build 50%  "},
		{8, "build 5…"},
		{0, ""},
		{-1, ""},
	} {
		p := themedBar(t, compactTheme(), halfDone(now))
		row, err := p.row(tc.width, now)
		assert.NoError(t, err)
		assert.Equal(t, tc.want, row.String())
		assert.True(t, row.Width() <= max(tc.width, 0))
	}
}

func TestThemedProgressASCIISymbols(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	p := themedBar(t, asciiTheme(false), halfDone(now))
	row, err := p.row(47, now)
	assert.NoError(t, err)
	// the elapsed time does not fit the hint row, which drops it whole
	assert.Equal(t, "build  50%  =================>-----------------\n"+
		"50 / 100 items | 50.00 items/s | 1s left", row.String())
	p = themedBar(t, compactTheme(), halfDone(now))
	row, err = p.row(47, now)
	assert.NoError(t, err)
	assert.Equal(t, "build 50%  ======>------ 50.00/s · 1s remaining", row.String())
}

func TestThemedProgressBarWidthBounds(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	th := compactTheme()
	th.Progress.MaxBarWidth = 4
	p := themedBar(t, th, halfDone(now))
	row, err := p.row(80, now)
	assert.NoError(t, err)
	assert.Equal(t, "build 50%  ==-- 50.00/s · 1s remaining · 1s elapsed", row.String())

	th = compactTheme()
	th.Progress.MinBarWidth = 10
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(47, now)
	assert.NoError(t, err)
	assert.Equal(t, "build 50%  ======>------ 50.00/s · 1s remaining", row.String())
	// a 6-column bar is below the minimum, so the remaining time goes
	row, err = p.row(40, now)
	assert.NoError(t, err)
	assert.Equal(t, "build 50%  ==========>---------- 50.00/s", row.String())

	// the stacked bar is bounded the same way; the hint has its own row
	th = asciiTheme(false)
	th.Progress.MaxBarWidth = 4
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(80, now)
	assert.NoError(t, err)
	assert.Equal(t, "build  50%  ==--\n50 / 100 items | 50.00 items/s | 1s left | 1s elapsed", row.String())
}

func TestThemedProgressHeadMarksPartialCell(t *testing.T) {
	l := &progressLook{style: theme.New().Progress, symbols: theme.New().Symbols.ASCII()}
	l.symbols.BarEmpty = "."
	for _, tc := range []struct {
		completion float64
		want       string
	}{
		{0, ".........."},
		{0.01, ">........."},
		{0.5, "=====....."},
		{0.55, "=====>...."},
		{0.99, "=========>"},
		{1, "=========="},
		{1.5, "=========="},
		{-0.5, ".........."},
	} {
		assert.Equal(t, tc.want, l.bar(10, tc.completion).String())
	}
	assert.Equal(t, "", l.bar(0, 0.5).String())
}

func TestThemedProgressUnknownAndExceededTotals(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		edit             func(*progressState)
		width            int
		stacked, compact string
	}{{
		// no total: an unknown percent, no estimate, an empty track
		"unknown total", func(s *progressState) { s.maxNum = 0 }, 30,
		"build  ?    ------------------\n50.00 items/s",
		"build ?    ----------- 50.00/s",
	}, {
		// past the total: full bar, nothing remaining, which is not unknown
		"exceeded total", func(s *progressState) { s.currentNum, s.sinceRedrawNum = 150, 150 }, 40,
		"build  100% ============================\n150 / 100 items | 50.00 items/s",
		"build 100% ====== 50.00/s · 0s remaining",
	}, {
		// no rate yet: the estimate is unknown, not zero
		"no rate", func(s *progressState) { s.rollingRates = nil }, 30,
		"build  50%  =========---------\n50 / 100 items | 0.00 items/s",
		"build 50%  ======------ 0.00/s",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			state := halfDone(now)
			state.showElapsed = false
			tc.edit(&state)
			row, err := themedBar(t, asciiTheme(false), state).row(tc.width, now)
			assert.NoError(t, err)
			assert.Equal(t, tc.stacked, row.String())
			row, err = themedBar(t, compactTheme(), state).row(tc.width, now)
			assert.NoError(t, err)
			assert.Equal(t, tc.compact, row.String())
		})
	}
}

func TestThemedProgressView(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	th := compactTheme()
	th.Templates.Progress.Label = `<{{ .Label }}>`
	th.Templates.Progress.Metrics = `{{ .Current }}/{{ .Total }}{{ with .Rate }} at {{ . }}{{ end }}` +
		`{{ with .Remaining }} eta {{ . }}{{ end }}{{ with .Elapsed }} after {{ . }}{{ end }}`
	th.Templates.Progress.Frame = `{{ .Label }} {{ .Percent }} [{{ .Bar }}] {{ .Metrics }} {{ len .Values }}`
	state := halfDone(now)
	state.fmtRate = func(r float64) string { return fmt.Sprintf("%.0f\nitems", r) }
	p := themedBar(t, th, state)
	p.label = ansi.Text("multi\nline\x1b[2K")
	row, err := p.row(80, now)
	assert.NoError(t, err)
	// the rate is formatted by WithFormatRate and kept on one line
	assert.Equal(
		t,
		"<multi line> 50 ["+strings.Repeat(
			"=",
			12,
		)+">"+strings.Repeat(
			"-",
			12,
		)+"] 50/100 at 50 items eta 1s after 1s 3",
		row.String(),
	)
	// the metrics slot sees only the metrics that fit
	row, err = p.row(50, now)
	assert.NoError(t, err)
	assert.Equal(t, "<multi line> 50 [==--] 50/100 at 50 items eta 1s 2", row.String())

	// a stacked frame gets no metrics; the hint row gets the pairs that fit
	th.Progress.Compact = false
	th.Templates.Progress.Hint = `{{ range . }}[{{ .Left }}{{ with .Right }} {{ . }}{{ end }}]{{ end }}`
	th.Templates.Progress.Frame = `{{ .Label }} {{ .Percent }} [{{ .Bar }}] {{ len .Hints }}/{{ len .Values }}{{ .Metrics }}`
	p = themedBar(t, th, state)
	p.label = ansi.Text("multi\nline\x1b[2K")
	row, err = p.row(40, now)
	assert.NoError(t, err)
	assert.Equal(t, "<multi line> 50 [=========---------] 3/2\n[50 / 100 items][50 items /s][1s left]", row.String())
}

func TestThemedProgressHiddenSlots(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	th := compactTheme()
	th.Templates.Progress.Label = theme.Hidden
	th.Templates.Progress.Metrics = theme.Hidden
	p := themedBar(t, th, halfDone(now))
	row, err := p.row(16, now)
	assert.NoError(t, err)
	assert.Equal(t, "50%  =====>-----", row.String())

	// hidden metrics leave the stacked hint alone, and the other way round
	th.Progress.Compact = false
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(16, now)
	assert.NoError(t, err)
	assert.Equal(t, "50%  =====>-----\n50 / 100 items", row.String())
	th.Templates.Progress.Hint = theme.Hidden
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(16, now)
	assert.NoError(t, err)
	assert.Equal(t, "50%  =====>-----", row.String())
	th.Templates.Progress.Metrics = ""
	th.Progress.Compact = true
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(40, now)
	assert.NoError(t, err)
	assert.Equal(t, "50%  ======------ 50.00/s · 1s remaining", row.String())
}

func TestThemedProgressFrameErrors(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	for _, tc := range []struct {
		funcs template.FuncMap
		frame theme.Template
		want  string
	}{
		{
			template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }},
			`{{ boom }}{{ .Bar }}`, "theme.Templates.Progress.Frame: template: Progress.Frame:1:3: executing",
		},
		{
			template.FuncMap{"raw": func(v any) string { return fmt.Sprint(v) }},
			`{{ .Bar }}{{ raw . }}`, "theme.Templates.Progress.Frame: want the bar once",
		},
		{
			template.FuncMap{"erase": func() string { return "\x1b[2K" }},
			`{{ erase }}{{ .Bar }}`, `theme.Templates.Progress.Frame: "\x1b[2K": want text and SGR only`,
		},
	} {
		th := theme.New()
		th.Funcs = tc.funcs
		th.Templates.Progress.Frame = tc.frame
		p := themedBar(t, th, halfDone(now))
		_, err := p.row(40, now)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestThemedProgressRoles(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	th := theme.New()
	th.Light = true
	c, err := render.Compile(th)
	assert.NoError(t, err)
	look := c.Bind(color.ProfileTrueColor, color.Background{Light: true})
	roles := look.Roles
	assert.True(t, len(roles.Accent) > 0 && len(roles.Guide) > 0 && len(roles.Muted) > 0 && len(roles.Foreground) > 0)
	p := &Progressbar{progressState: halfDone(now), label: ansi.Text("build"), theme: c}
	p.look, err = p.bind(look)
	assert.NoError(t, err)
	rows, err := p.rows(60, 0, now)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(rows))
	bar, hint := rows[0].String(), rows[1].String()
	assert.Contains(t, bar, string(roles.Foreground)+"build")
	assert.Contains(t, bar, string(roles.Accent)+"50% ")
	assert.Contains(t, bar, string(roles.Accent)+strings.Repeat("━", 24)+color.FgDefault+
		string(roles.Guide)+strings.Repeat("─", 24)+color.FgDefault)
	// the hint sets the values apart from their units and separators
	assert.Contains(
		t,
		hint,
		string(roles.Accent)+"50 / 100"+color.FgDefault+" "+string(roles.Guide)+"items"+color.FgDefault,
	)
	assert.Contains(t, hint, string(roles.Guide)+" · "+color.FgDefault+string(roles.Accent)+"50.00"+color.FgDefault)
	assert.Equal(t, "50 / 100 items · 50.00 items/s · 1s left · 1s elapsed", stripSGR(hint))
	// running work is not a success or a failure
	for _, row := range rows {
		out := row.String()
		assert.True(t, !strings.Contains(out, string(roles.Success)) && !strings.Contains(out, string(roles.Failure)))
	}
	assert.Equal(t, 60, rows[0].Width())
}

func TestThemedProgressCompletes(t *testing.T) {
	p, frames, tick, restored := themedProgress(t, asciiTheme(false), 100)
	p.Add(50)
	tick(time.Second)
	// the estimate does not fit the hint row, which drops it whole
	assert.Equal(t, frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"50 / 100 items | 50.00 items/s"), <-frames)
	p.Add(50)
	tick(2 * time.Second)
	assert.Equal(t, erased(t, 2)+frameOf("build  100% "+strings.Repeat("=", 27),
		"100 / 100 items | 50.00 items/s"), <-frames)
	// done: both rows are erased, and no completion line is retained
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, p, frames))
	assert.True(t, restored.Load())
	assert.NoError(t, p.Close())
}

func TestThemedProgressCloseBeforeDone(t *testing.T) {
	p, frames, tick, restored := themedProgress(t, asciiTheme(false), 100)
	p.Add(5)
	tick(time.Second)
	assert.Equal(t, frameOf("build  5%   =>"+strings.Repeat("-", 25), "5 / 100 items | 0.00 items/s"), <-frames)
	closed := make(chan error)
	go func() { closed <- p.Close() }()
	// stopping looks like completing: the rows are erased
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, p, frames))
	assert.NoError(t, <-closed)
	assert.True(t, restored.Load())
}

func TestThemedProgressResize(t *testing.T) {
	p, frames, tick, _ := themedProgress(t, asciiTheme(false), 100)
	p.Add(50)
	tick(time.Second)
	assert.Equal(t, frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"50 / 100 items | 50.00 items/s"), <-frames)
	// the 39-column bar row and the 30-column hint row each wrap on 20
	// columns, so four rows are cleared
	p.io.Width = 20
	tick(2 * time.Second)
	assert.Equal(t, erased(t, 4)+frameOf("build  50%  ===>---", "50 / 100 items"), <-frames)
	// the hint row goes once nothing fits
	p.io.Width = 14
	tick(3 * time.Second)
	assert.Equal(t, erased(t, 3)+frameOf("build  50%  >"), <-frames)
	p.io.Width = 40
	tick(4 * time.Second)
	assert.Equal(t, erased(t, 1)+frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"50 / 100 items | 50.00 items/s"), <-frames)
}

func TestThemedProgressShortTerminal(t *testing.T) {
	p, frames, tick, _ := themedProgress(t, asciiTheme(false), 100)
	p.Add(50)
	// one row has no room for the hint, so the metrics join the bar row
	p.io.Height = 1
	tick(time.Second)
	assert.Equal(t, frameOf("build  50%  ==-- 50.00/s · 1s remaining"), <-frames)
	// nor do two, since the cursor takes the row below the frame
	p.io.Height = 2
	tick(time.Second)
	assert.Equal(t, erased(t, 1)+frameOf("build  50%  ==-- 50.00/s · 1s remaining"), <-frames)
	// a taller terminal gets the hint row back
	p.io.Height = 3
	tick(2 * time.Second)
	assert.Equal(t, erased(t, 1)+frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"50 / 100 items | 50.00 items/s"), <-frames)
}

func TestThemedProgressLongLabel(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	for _, tc := range []struct {
		width int
		want  string
	}{
		// the label is shortened, by columns, before the bar goes
		{40, "ファイル build  50%  =========>---------"},
		{30, "ファイル build  50%  ====>----"},
		{24, "ファイル build  50%  =>-"},
		{20, "ファイル bu…  50%  >"},
		{17, "ファイル…  50%  >"},
		// a wide character that does not fit leaves its column to the bar
		{14, "ファ…  50%  =-"},
		// too narrow for an ellipsis and a label: the label goes
		{9, "50%  ==--"},
		// too narrow for the bar: the start of the row
		{4, "50%…"},
	} {
		p := themedBar(t, asciiTheme(false), halfDone(now))
		p.label = ansi.Text("ファイル build")
		p.showRate, p.showEstimate, p.showElapsed = false, false, false
		rows, err := p.rows(tc.width, 0, now)
		assert.NoError(t, err)
		assert.Equal(t, tc.want, rows[0].String())
		assert.True(t, rows[0].Width() <= tc.width)
	}
}

func TestThemedProgressRenderErrorEndsBar(t *testing.T) {
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Progress.Label = `{{ if eq .Current 150 }}{{ boom }}{{ end }}{{ .Label }}`
	p, frames, tick, restored := themedProgress(t, th, 200)
	p.Add(100)
	tick(time.Second)
	assert.Equal(t, frameOf("build  50%  "+strings.Repeat("=", 13)+">"+strings.Repeat("-", 13),
		"100 / 200 items | 100.00 items/s"), <-frames)
	p.Add(50)
	tick(2 * time.Second)
	// no partial frame: only the drawn rows are cleared
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, p, frames))
	assert.True(t, restored.Load())
	err := p.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redraw: theme.Templates.Progress.Label")
	assert.Contains(t, err.Error(), "boom")
	// nobody renders anymore, and Add does not block
	p.Add(1)
}

func TestThemedProgressWriteErrorEndsBar(t *testing.T) {
	terst.UseTheme(t, theme.New())
	restored := false
	p, err := NewMaxProgressBar(t.Context(), "build", 10, option.OpT(func(pb *Progressbar) error {
		pb.ticks = time.After(0)
		pb.makeTermIO = func(in io.Reader, _ io.Writer) (*term.IO, error) {
			return &term.IO{In: in, Out: errWriterPB{}, Width: 40, Height: 1, Restore: func() error {
				restored = true
				return nil
			}}, nil
		}
		return nil
	}))
	assert.NoError(t, err)
	<-p.stopped
	err = p.Close()
	assert.ErrorIs(t, err, io.EOF)
	assert.True(t, restored)
}

func TestThemedProgressWithoutTerminal(t *testing.T) {
	th := theme.New()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Progress.Label = `{{ boom }}`
	th.Templates.Progress.Metrics = `{{ boom }}`
	th.Templates.Progress.Frame = `{{ boom }}{{ .Bar }}`
	terst.UseTheme(t, th)
	noTTY := option.OpT(func(pb *Progressbar) error {
		pb.makeTermIO = func(io.Reader, io.Writer) (*term.IO, error) { return nil, ErrNoTTY }
		return nil
	})
	p, err := NewMaxProgressBar(t.Context(), "build", 3, noTTY)
	assert.NoError(t, err)
	p.Add(3)
	assert.NoError(t, p.Close())
	var got []int
	for v, err := range NewSliceProgressBar(t.Context(), "items", []int{1, 2}, noTTY) {
		assert.NoError(t, err)
		got = append(got, v)
	}
	assert.Equal(t, []int{1, 2}, got)
	var sum atomic.Int64
	err = NewParallelProgressBar(t.Context(), "items", []int{1, 2, 3}, func(v int) error {
		sum.Add(int64(v))
		return nil
	}, WithWorkers(2), noTTY)
	assert.NoError(t, err)
	assert.Equal(t, int64(6), sum.Load())
}

func TestThemedProgressCapturesTheme(t *testing.T) {
	th := asciiTheme(false)
	th.Symbols.BarFill = "#"
	p, frames, tick, _ := themedProgress(t, th, 100)
	// a theme installed later does not reach the running bar
	later := asciiTheme(false)
	later.Symbols.BarFill = "="
	later.Progress.Compact = true
	assert.NoError(t, term.Default.InstallTheme(later))
	p.Add(60)
	tick(time.Second)
	assert.Equal(t, frameOf("build  60%  "+strings.Repeat("#", 16)+">"+strings.Repeat("-", 10),
		"60 / 100 items | 60.00 items/s"), <-frames)
}
