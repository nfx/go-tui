// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// progressPercent is the completion of a bar left-aligned in four columns,
// or the Unknown symbol without a total.
const progressPercent = `{{ if gt .Total 0 }}{{ accent (printf "%-4s" (printf "%d%%" .Percent)) }}` +
	`{{ else }}{{ accent (printf "%-4s" (symbol "Unknown")) }}{{ end }}`

// stackedProgressFrame is the built-in frame of bars that are not
// [ProgressStyle.Compact], which sets the label apart from the percentage.
const stackedProgressFrame = `{{ with .Label }}{{ . }}  {{ end }}` + progressPercent +
	` {{ .Bar }}{{ with .Metrics }} {{ . }}{{ end }}`

// builtins returns the built-in templates with the frame of the layout.
func (s ProgressStyle) builtins() Templates {
	t := builtinTemplates
	if !s.Compact {
		t.Progress.Frame = stackedProgressFrame
	}
	return t
}

// progressView is the data of every progress slot but Hint, from one
// snapshot of the state. Rate, Remaining and Elapsed are empty when dropped
// to fit the row; Remaining also while unknown, which is distinct from "0s".
type progressView struct {
	// Label is the caller label on one line; in Frame, the Label slot.
	Label string
	// Metrics is the Metrics slot, in the Frame of [ProgressStyle.Compact]
	// bars only.
	Metrics string
	// Bar is the bar, sized to the row, in Frame only.
	Bar     string
	Current int64
	// Total is the size of the work; not positive when unknown.
	Total int64
	// Percent is the completion, 0 to 100; 0 without a positive Total.
	Percent int
	// Amount is the bytes read of a file and, with a positive Total, its
	// size, in binary units: "3.0 MiB / 10.0 MiB". Empty for counted bars.
	Amount string
	// Rate is formatted by [WithFormatRate], without the "/s" unit; for a
	// file, in binary units without it.
	Rate      string
	Remaining string
	Elapsed   string
	// Values are the shown metrics with their units, in the order they are
	// kept: amount, rate, remaining and elapsed time.
	Values []string
	// Hints are the shown metrics as pairs of a value and its unit, in the
	// same order, led by the count of a counted bar with a total. The Hint
	// slot renders them.
	Hints hintView
}

// progressMetric is one metric of a snapshot: its value for the legacy
// field of its kind, the value with its units for Values, and its pair for
// Hints. A metric with only a pair, such as the count of a counted bar, has
// no kind.
type progressMetric struct {
	kind  progressMetricKind
	value string
	shown string
	pair  hint
}

// progressMetricKind names the legacy field of a metric.
type progressMetricKind int

const (
	pairOnly progressMetricKind = iota
	amountMetric
	rateMetric
	remainingMetric
	elapsedMetric
)

// metrics returns every available metric of the current state, in the
// order they are kept: amount, rate, remaining and elapsed time.
func (p *Progressbar) metrics(rate float64) []progressMetric {
	var ms []progressMetric
	switch {
	case p.bytes:
		amount := p.byteSize(float64(p.currentNum))
		if p.maxNum > 0 {
			amount += " / " + p.byteSize(float64(p.maxNum))
		}
		ms = append(ms, progressMetric{kind: amountMetric, value: amount, shown: amount, pair: p.amountPair()})
	case p.maxNum > 0:
		count := hint{Left: text(fmt.Sprintf("%d / %d", p.currentNum, p.maxNum)), Right: text("items")}
		ms = append(ms, progressMetric{pair: count})
	}
	if p.showRate {
		ms = append(ms, p.rateMetric(rate))
	}
	if p.showEstimate && rate > 0 && p.maxNum > 0 {
		d := p.remainingSeconds(rate).String()
		ms = append(ms, progressMetric{
			kind: remainingMetric, value: d, shown: d + " remaining",
			pair: hint{Left: text(d), Right: text("left")},
		})
	}
	if p.showElapsed {
		d := p.elapsed.Truncate(time.Second).String()
		ms = append(ms, progressMetric{
			kind: elapsedMetric, value: d, shown: d + " elapsed",
			pair: hint{Left: text(d), Right: text("elapsed")},
		})
	}
	return ms
}

// amountPair is the bytes read and the size of a file. Amounts in one unit
// share it; otherwise each keeps its own, and the pair has no right side.
func (p *Progressbar) amountPair() hint {
	read, unit := p.byteParts(float64(p.currentNum))
	if p.maxNum <= 0 {
		return hint{Left: text(read), Right: text(unit)}
	}
	size, sizeUnit := p.byteParts(float64(p.maxNum))
	if unit != sizeUnit {
		return hint{Left: text(read + " " + unit + " / " + size + " " + sizeUnit)}
	}
	return hint{Left: text(read + " / " + size), Right: text(unit)}
}

// rateMetric is the rate of the bar: bytes per second for a file, unless
// [WithFormatRate] formats it, which then owns the unit before "/s".
func (p *Progressbar) rateMetric(rate float64) progressMetric {
	if p.bytes && p.fmtRate == nil {
		value, unit := p.byteParts(rate)
		shown := value + " " + unit
		return progressMetric{
			kind: rateMetric, value: shown, shown: shown + "/s",
			pair: hint{Left: text(value), Right: text(unit + "/s")},
		}
	}
	value := text(p.formatRate(rate)).oneLine()
	right := "/s"
	if p.fmtRate == nil {
		right = "items/s"
	}
	return progressMetric{
		kind: rateMetric, value: value, shown: value + "/s",
		pair: hint{Left: text(value), Right: text(right)},
	}
}

// view returns the progress data with the metrics ms shown.
func (p *Progressbar) view(ms []progressMetric) progressView {
	v := progressView{
		Label:   p.label.oneLine(),
		Current: p.currentNum,
		Total:   p.maxNum,
		Percent: min(100, max(0, int(p.completion()*100))),
	}
	for _, m := range ms {
		v.Hints = append(v.Hints, m.pair)
		var field *string
		switch m.kind {
		case amountMetric:
			field = &v.Amount
		case rateMetric:
			field = &v.Rate
		case remainingMetric:
			field = &v.Remaining
		case elapsedMetric:
			field = &v.Elapsed
		default:
			continue
		}
		*field = m.value
		v.Values = append(v.Values, m.shown)
	}
	return v
}

// rows renders the bar in width columns and at most height rows from one
// snapshot of the state, in the layout of the theme. A height below two
// rows takes the compact row; a height that is not positive is unknown.
func (p *Progressbar) rows(width, height int, now time.Time) ([]text, error) {
	p.increment(now)
	if width <= 0 {
		return []text{nil}, nil
	}
	ms := p.metrics(p.rollingRate())
	if !p.look.style.Compact && (height <= 0 || height >= 2) {
		return p.stacked(width, ms)
	}
	row, err := p.compact(width, ms)
	return []text{row}, err
}

// compact renders the single row of the bar in width columns. It drops
// metrics, the last first, until the bar keeps MinBarWidth; the bar then
// takes the columns the rest of the frame leaves, up to MaxBarWidth. A row
// that does not fit even without metrics is clipped.
func (p *Progressbar) compact(width int, ms []progressMetric) (text, error) {
	for n := len(ms); n >= 0; n-- {
		v := p.view(ms[:n])
		label, err := p.look.label.render(v)
		if err != nil {
			return nil, err
		}
		metrics, err := p.look.metrics.render(v)
		if err != nil {
			return nil, err
		}
		before, after, err := p.look.around(v, label, metrics)
		if err != nil {
			return nil, err
		}
		bar := p.look.barWidth(width - before.width() - after.width())
		if bar < p.look.style.MinBarWidth && n > 0 {
			continue
		}
		return p.look.fill(width, before, bar, after, p.completion()), nil
	}
	return nil, nil
}

// stacked renders the bar row and, below it, the hint row of the metrics
// that fit, each in width columns. The hint drops whole metrics, the last
// first, and is omitted when none fits. The label is shortened, and then
// dropped, before the bar goes below MinBarWidth; a row that does not fit
// even without the label is clipped.
func (p *Progressbar) stacked(width int, ms []progressMetric) ([]text, error) {
	hint, n, err := p.look.fitHint(width, ms)
	if err != nil {
		return nil, err
	}
	v := p.view(ms[:n])
	label, err := p.look.label.render(v)
	if err != nil {
		return nil, err
	}
	for {
		before, after, err := p.look.around(v, label, nil)
		if err != nil {
			return nil, err
		}
		bar := p.look.barWidth(width - before.width() - after.width())
		if short := p.look.style.MinBarWidth - bar; short > 0 && len(label) > 0 {
			// one column of what is left goes to the ellipsis
			if keep := label.width() - short; keep >= 2 {
				label = label.truncateColumns(keep)
			} else {
				label = nil
			}
			continue
		}
		row := p.look.fill(width, before, bar, after, p.completion())
		if hint.width() == 0 {
			return []text{row}, nil
		}
		return []text{row, hint}, nil
	}
}

// byteUnits are the binary units above bytes.
var byteUnits = [...]string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

// byteSize formats n bytes in binary units: whole bytes below 1 KiB, then
// one decimal, as "512 B" or "1.5 MiB".
func (p *Progressbar) byteSize(n float64) string {
	value, unit := p.byteParts(n)
	return value + " " + unit
}

// byteParts is [Progressbar.byteSize] apart from its unit.
func (p *Progressbar) byteParts(n float64) (value, unit string) {
	if math.Round(n) < 1024 {
		return fmt.Sprintf("%.0f", n), "B"
	}
	n /= 1024
	i := 0
	// move up once the value would print as 1024.0
	for i < len(byteUnits)-1 && math.Round(n*10) >= 1024*10 {
		n /= 1024
		i++
	}
	return fmt.Sprintf("%.1f", n), byteUnits[i]
}

// progressBarMark stands in for the bar while the frame renders. Slots
// cannot emit it, since it is a control character.
const progressBarMark = "\x00"

// progressLook is the theme of one bar, bound to its destination.
type progressLook struct {
	binding                     binding
	style                       ProgressStyle
	symbols                     Symbols
	label, metrics, hint, frame boundSlot
}

// bindLook binds the captured theme to the output of the bar. Output
// only: without a lease, it never probes the terminal.
func (p *Progressbar) bindLook(ctx context.Context) (err error) {
	p.look, err = p.bind(defaultIO.bindTheme(ctx, p.theme, p.out, nil))
	return err
}

// bind attaches the destination helpers of look to every progress slot.
func (p *Progressbar) bind(look binding) (*progressLook, error) {
	t := p.theme.templates.Progress
	l := &progressLook{binding: look, style: p.theme.source.Progress, symbols: p.theme.source.Symbols}
	var errs [4]error
	l.label, errs[0] = t.Label.bind(look.funcs)
	l.metrics, errs[1] = t.Metrics.bind(look.funcs)
	l.hint, errs[2] = t.Hint.bind(look.funcs)
	l.frame, errs[3] = t.Frame.bind(look.funcs)
	return l, errors.Join(errs[:]...)
}

// fitHint renders the hint of the first metrics of ms that fit in width
// columns, the last dropped first, and returns how many it shows. Without
// a fitting metric, the hint is empty and shows none; a hidden hint shows
// all of them.
func (l *progressLook) fitHint(width int, ms []progressMetric) (text, int, error) {
	if l.hint.tmpl == nil {
		return nil, len(ms), nil
	}
	pairs := make(hintView, len(ms))
	for i, m := range ms {
		pairs[i] = m.pair
	}
	for n := len(pairs); n > 0; n-- {
		out, err := l.hint.render(pairs[:n])
		if err != nil {
			return nil, 0, err
		}
		if out.width() <= width {
			return out, n, nil
		}
	}
	return nil, 0, nil
}

// barWidth bounds the columns left for the bar by MaxBarWidth.
func (l *progressLook) barWidth(cols int) int {
	if l.style.MaxBarWidth > 0 {
		return min(cols, l.style.MaxBarWidth)
	}
	return cols
}

// fill joins the frame around a bar of width cells at completion, clipped
// to cols columns.
func (l *progressLook) fill(cols int, before text, width int, after text, completion float64) text {
	row := slices.Concat(before, l.bar(max(width, 0), completion), after)
	if row.width() > cols {
		row = row.truncateColumns(cols)
	}
	return row
}

// around renders the frame of v with the rendered label and metrics, and
// returns it before and after the bar.
func (l *progressLook) around(v progressView, label, metrics text) (before, after text, err error) {
	v.Label, v.Metrics, v.Bar = label.String(), metrics.String(), progressBarMark
	out, err := l.frame.execute(v)
	if err != nil {
		return nil, nil, err
	}
	before, after, ok := bytes.Cut(out, []byte(progressBarMark))
	if !ok || bytes.Contains(after, []byte(progressBarMark)) {
		return nil, nil, fmt.Errorf("theme.Templates.%s: want the bar once", l.frame.tmpl.Name())
	}
	for _, part := range [][]byte{before, after} {
		if err := l.frame.styled(part); err != nil {
			return nil, nil, err
		}
	}
	return before, after, nil
}

// bar draws width cells at completion: the done cells and the head, which
// marks a partly done cell, in the accent role, then the track in the guide
// role.
func (l *progressLook) bar(width int, completion float64) text {
	// tolerate rounding, so that 5% of 20 cells is one cell without a head
	const epsilon = 1e-9
	exact := float64(width) * completion
	done := min(width, max(0, int(exact+epsilon)))
	cells := strings.Repeat(l.symbols.BarFill, done)
	if exact-float64(done) > epsilon && done < width {
		cells += l.symbols.BarHead
		done++
	}
	return slices.Concat(
		l.binding.style(l.binding.roles.accent, text(cells)),
		l.binding.style(l.binding.roles.guide, text(strings.Repeat(l.symbols.BarEmpty, width-done))),
	)
}
