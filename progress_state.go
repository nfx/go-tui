// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"time"
)

type progressState struct {
	maxNum         int64
	currentNum     int64
	sinceRedrawNum int64
	redrawAt       time.Time
	startedAt      time.Time
	rollingRates   []float64
	elapsed        time.Duration
	fmtRate        func(float64) string
	showEstimate   bool
	showRate       bool
	showElapsed    bool
	// bytes counts the bytes of a file, shown in binary units
	bytes bool
}

func (p *progressState) isDone() bool {
	if p.maxNum <= 0 {
		return false
	}
	return p.currentNum >= p.maxNum
}

// completion returns 0..1, or 0 when the maximum is not positive.
func (p *progressState) completion() float64 {
	if p.maxNum <= 0 {
		return 0
	}
	return float64(p.currentNum) / float64(p.maxNum)
}

// formatRate formats a rate with [WithFormatRate], without its unit.
func (p *progressState) formatRate(rate float64) string {
	if p.fmtRate == nil {
		return fmt.Sprintf("%.2f", rate)
	}
	return p.fmtRate(rate)
}

func (p *progressState) increment(now time.Time) {
	p.elapsed = now.Sub(p.startedAt)
	increment := p.currentNum - p.sinceRedrawNum
	if increment == 0 {
		return
	}
	p.sinceRedrawNum = p.currentNum
	since := now.Sub(p.redrawAt)
	completionRate := float64(increment) / since.Seconds()
	p.rollingRates = append(p.rollingRates, completionRate)
	keep := min(10, int(p.currentNum/10)) // keep last max 10% of rates
	if len(p.rollingRates) > keep {
		p.rollingRates = p.rollingRates[1:] // keep only the last 10 rates
	}
	p.redrawAt = now
}

func (p *progressState) remainingSeconds(rollingRate float64) time.Duration {
	remainingNum := p.maxNum - p.currentNum
	if remainingNum <= 0 {
		return 0
	}
	remainingTime := time.Duration(float64(remainingNum)/rollingRate*1) * time.Second
	return remainingTime
}

func (p *progressState) rollingRate() float64 {
	if len(p.rollingRates) == 0 {
		return 0.0
	}
	var sum float64
	for _, rate := range p.rollingRates {
		sum += rate
	}
	return sum / float64(len(p.rollingRates))
}

func (p *progressState) metricsSnapshot() progressUpdate {
	rate := p.rollingRate()
	remaining := int64(-1) // estimate unavailable until a rate is known
	if rate > 0 {
		remaining = int64(p.remainingSeconds(rate).Seconds())
	}
	return progressUpdate{
		Complete:  p.completion(),
		Rate:      rate,
		Remaining: remaining,
		Elapsed:   int64(p.elapsed.Seconds()),
	}
}
