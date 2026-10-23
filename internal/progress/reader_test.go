// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package progress

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/iotest"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestWrapReaderSizeFallback(t *testing.T) {
	r := &WrapReader{r: &sizedReader{size: 42}}

	size, err := r.Size()
	assert.NoError(t, err)
	assert.Equal(t, int64(42), size)
}

func TestWrapReaderSizeUnknown(t *testing.T) {
	_, err := NewFileProgressReader(t.Context(), bytes.NewBufferString("no size"), "file")
	assert.ErrorIs(t, err, errNoSize)
}

func TestWrapReaderCloseCallsUnderlying(t *testing.T) {
	cr := &closeReader{}
	w := &WrapReader{r: cr, p: &Progressbar{}}
	err := w.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cr.closed {
		t.Fatalf("expected close to be called")
	}
}

func TestWrapReaderCloseProgressError(t *testing.T) {
	cr := &closeReader{}
	p := &Progressbar{io: &term.IO{}, cancel: func(error) {}, err: io.EOF}
	w := &WrapReader{r: cr, p: p}
	err := w.Close()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	if !cr.closed {
		t.Fatalf("expected underlying close despite progress error")
	}
}

func TestThemedFileProgressByteMetrics(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	const amount, rate = " 5.0 MiB / 10.0 MiB", " · 1.0 MiB/s"
	for _, tc := range []struct {
		width int
		want  string
	}{
		{80, amount + rate + " · 5s remaining · 1s elapsed"},
		{60, amount + rate + " · 5s remaining"},
		{45, amount + rate},
		{35, amount},
		{30, ""},
	} {
		p := themedBar(t, compactTheme(), halfFile(now))
		row, err := p.row(tc.width, now)
		assert.NoError(t, err)
		// the amount is the last metric to go
		assert.Equal(t, tc.want, afterTrack(row))
		assert.Equal(t, tc.width, row.Width())
	}

	// without a total, the bytes read alone
	state := halfFile(now)
	state.maxNum, state.showElapsed = 0, false
	p := themedBar(t, compactTheme(), state)
	row, err := p.row(40, now)
	assert.NoError(t, err)
	assert.Equal(t, "build ?    "+strings.Repeat("-", 9)+" 5.0 MiB · 1.0 MiB/s", row.String())

	// WithFormatRate formats the rate of a file too
	state = halfFile(now)
	state.fmtRate = func(r float64) string { return fmt.Sprintf("%.0f bytes", r) }
	th := compactTheme()
	th.Progress.Metrics = `{{ .Amount }}|{{ .Rate }}|{{ .Current }}/{{ .Total }}`
	p = themedBar(t, th, state)
	row, err = p.row(80, now)
	assert.NoError(t, err)
	assert.Equal(t, " 5.0 MiB / 10.0 MiB|1048576 bytes|5242880/10485760", afterTrack(row))

	// a counted bar has no amount
	p = themedBar(t, th, halfDone(now))
	row, err = p.row(80, now)
	assert.NoError(t, err)
	assert.Equal(t, " |50.00|50/100", afterTrack(row))
}

func TestThemedFileProgressHint(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC)
	const amount, rate = "5.0 / 10.0 MiB", " | 1.0 MiB/s"
	for _, tc := range []struct {
		width int
		want  string
	}{
		{80, amount + rate + " | 5s left | 1s elapsed"},
		{40, amount + rate + " | 5s left"},
		{30, amount + rate},
		{20, amount},
		{13, ""},
	} {
		rows, err := themedBar(t, terst.ASCIITheme(false), halfFile(now)).rows(tc.width, 0, now)
		assert.NoError(t, err)
		// the bar keeps its geometry, whatever the hint shows
		assert.Equal(t, "build  50%  ", rows[0][:12].String())
		hint := ""
		if len(rows) > 1 {
			hint = rows[1].String()
		}
		assert.Equal(t, tc.want, hint)
	}
	for _, tc := range []struct {
		state func(*progressState)
		want  string
	}{
		// amounts in different units keep their own
		{func(s *progressState) { s.currentNum, s.sinceRedrawNum = 900<<10, 900<<10 }, "900.0 KiB / 10.0 MiB | 1.0 MiB/s"},
		{func(s *progressState) { s.maxNum, s.currentNum, s.sinceRedrawNum = 1000, 512, 512 }, "512 / 1000 B | 1.0 MiB/s"},
		// without a total, the bytes read alone
		{func(s *progressState) { s.maxNum = 0 }, "5.0 MiB | 1.0 MiB/s"},
		// WithFormatRate owns the unit of the rate
		{func(s *progressState) { s.fmtRate = func(r float64) string { return fmt.Sprintf("%.0f bytes", r) } },
			"5.0 / 10.0 MiB | 1048576 bytes /s"},
	} {
		state := halfFile(now)
		state.showEstimate, state.showElapsed = false, false
		tc.state(&state)
		rows, err := themedBar(t, terst.ASCIITheme(false), state).rows(80, 0, now)
		assert.NoError(t, err)
		assert.Equal(t, tc.want, rows[1].String())
	}
}

func TestThemedFileProgressFinalEOF(t *testing.T) {
	src := &scriptedFile{size: 4 << 20, reads: []fileRead{{1 << 20, nil}, {3 << 20, io.EOF}}}
	r, ft := themedFile(t, terst.ASCIITheme(false), src)
	buf := make([]byte, 4<<20)
	n, err := r.Read(buf)
	assert.Equal(t, 1<<20, n)
	assert.NoError(t, err)
	ft.tick(time.Second)
	assert.Equal(t, frameOf("build  25%  "+strings.Repeat("=", 11)+">"+strings.Repeat("-", 35),
		"1.0 / 4.0 MiB | 1.0 MiB/s | 3s left"), <-ft.frames)
	// the last bytes come with EOF, exactly as read
	n, err = r.Read(buf)
	assert.Equal(t, 3<<20, n)
	assert.ErrorIs(t, err, io.EOF)
	// EOF ends the bar: its rows are erased, with no completion line
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, r.p, ft.frames))
	assert.True(t, ft.restored.Load())
	assert.NoError(t, r.Close())
	// no read to find out that the file is complete
	assert.Equal(t, 2, src.calls)
}

func TestThemedFileProgressEarlyEOF(t *testing.T) {
	src := &scriptedFile{size: 100, reads: []fileRead{{30, io.EOF}}}
	r, ft := themedFile(t, terst.ASCIITheme(false), src)
	n, err := r.Read(make([]byte, 100))
	assert.Equal(t, 30, n)
	assert.ErrorIs(t, err, io.EOF)
	// a short file ends the bar without a frame claiming it complete
	assert.Equal(t, "", untilProgressStopped(t, r.p, ft.frames))
	assert.True(t, ft.restored.Load())
	assert.NoError(t, r.Close())
	assert.Equal(t, 1, src.calls)
}

func TestThemedFileProgressReadError(t *testing.T) {
	broken := errors.New("broken")
	src := &scriptedFile{size: 100, reads: []fileRead{{30, nil}, {10, broken}}}
	r, ft := themedFile(t, terst.ASCIITheme(false), src)
	buf := make([]byte, 100)
	_, err := r.Read(buf)
	assert.NoError(t, err)
	n, err := r.Read(buf)
	// the partial bytes and the error, as read
	assert.Equal(t, 10, n)
	assert.ErrorIs(t, err, broken)
	ft.tick(time.Second)
	// the partial bytes count; the error is the caller's to report
	assert.Equal(t, frameOf("build  40%  "+strings.Repeat("=", 18)+">"+strings.Repeat("-", 28),
		"40 / 100 B | 40 B/s | 1s left"), <-ft.frames)
	closed := make(chan error)
	go func() { closed <- r.Close() }()
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, r.p, ft.frames))
	assert.NoError(t, <-closed)
	assert.True(t, ft.restored.Load())
	assert.Equal(t, 2, src.calls)
}

func TestThemedFileProgressZeroSize(t *testing.T) {
	src := &scriptedFile{size: 0}
	r, ft := themedFile(t, terst.ASCIITheme(false), src)
	ft.tick(time.Second)
	// an empty file has no completion to show
	assert.Equal(t, frameOf("build  ?    "+strings.Repeat("-", 47), "0 B | 0 B/s"), <-ft.frames)
	n, err := r.Read(make([]byte, 8))
	assert.Equal(t, 0, n)
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, erased(t, 2), untilProgressStopped(t, r.p, ft.frames))
	assert.NoError(t, r.Close())
}

func TestThemedFileProgressRenderError(t *testing.T) {
	th := theme.New()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Progress.Hint = `{{ boom }}`
	src := &scriptedFile{size: 100, reads: []fileRead{{30, nil}, {20, nil}}}
	r, ft := themedFile(t, th, src)
	buf := make([]byte, 100)
	_, err := r.Read(buf)
	assert.NoError(t, err)
	ft.tick(time.Second)
	assert.Equal(t, "", untilProgressStopped(t, r.p, ft.frames))
	assert.True(t, ft.restored.Load())
	// reading goes on: the bar no longer receives bytes, and nothing blocks
	n, err := r.Read(buf)
	assert.Equal(t, 20, n)
	assert.NoError(t, err)
	err = r.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "progress: redraw: theme.Progress.Hint")
}

func TestThemedFileProgressLogsWithoutTerminal(t *testing.T) {
	th := theme.New()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Progress.Label = `{{ boom }}`
	th.Progress.Metrics = `{{ boom }}`
	th.Progress.Frame = `{{ boom }}{{ .Bar }}`
	terst.UseTheme(t, th)
	var logs, out bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	terst.UseIO(t, nil, &out)
	r, err := NewFileProgressReader(t.Context(), &progressSizedReader{
		Reader: strings.NewReader(strings.Repeat("x", 100)), size: 100,
	}, "Download")
	assert.NoError(t, err)
	data, err := io.ReadAll(iotest.OneByteReader(r))
	assert.NoError(t, err)
	assert.Equal(t, 100, len(data))
	assert.NoError(t, r.Close())
	// the same milestones as without a theme, and nothing on the output
	assert.Equal(t, "", out.String())
	var percentages []int
	decoder := json.NewDecoder(&logs)
	for decoder.More() {
		var entry struct {
			Percent int `json:"percent"`
		}
		assert.NoError(t, decoder.Decode(&entry))
		percentages = append(percentages, entry.Percent)
	}
	assert.Equal(t, []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}, percentages)
}
