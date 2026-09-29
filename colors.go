// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"
	"text/template"
)

const (
	reset = "\x1b[0m"

	// Colors.
	black  = "\x1b[30m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
	purple = "\x1b[35m"
	cyan   = "\x1b[36m"
	white  = "\x1b[37m"

	// Bright colors.
	brightBlack  = "\x1b[90m"
	brightRed    = "\x1b[91m"
	brightGreen  = "\x1b[92m"
	brightYellow = "\x1b[93m"
	brightBlue   = "\x1b[94m"
	brightPurple = "\x1b[95m"
	brightCyan   = "\x1b[96m"
	brightWhite  = "\x1b[97m"

	// Background colors.
	bgBlack  = "\x1b[40m"
	bgRed    = "\x1b[41m"
	bgGreen  = "\x1b[42m"
	bgYellow = "\x1b[43m"
	bgBlue   = "\x1b[44m"
	bgPurple = "\x1b[45m"
	bgCyan   = "\x1b[46m"
	bgWhite  = "\x1b[47m"

	// Text combineting.
	bold          = "\x1b[1m"
	dim           = "\x1b[2m"
	italic        = "\x1b[3m"
	underline     = "\x1b[4m"
	strikethrough = "\x1b[9m"
)

var colorFns = template.FuncMap{
	"black":        ansciiFormatter(black),
	"red":          ansciiFormatter(red),
	"green":        ansciiFormatter(green),
	"yellow":       ansciiFormatter(yellow),
	"blue":         ansciiFormatter(blue),
	"purple":       ansciiFormatter(purple),
	"cyan":         ansciiFormatter(cyan),
	"white":        ansciiFormatter(white),
	"brightBlack":  ansciiFormatter(brightBlack),
	"brightRed":    ansciiFormatter(brightRed),
	"brightGreen":  ansciiFormatter(brightGreen),
	"brightYellow": ansciiFormatter(brightYellow),
	"brightBlue":   ansciiFormatter(brightBlue),
	"brightPurple": ansciiFormatter(brightPurple),
	"brightCyan":   ansciiFormatter(brightCyan),
	"brightWhite":  ansciiFormatter(brightWhite),
	"bgBlack":      ansciiFormatter(bgBlack),
	"bgRed":        ansciiFormatter(bgRed),
	"bgGreen":      ansciiFormatter(bgGreen),
	"bgYellow":     ansciiFormatter(bgYellow),
	"bgBlue":       ansciiFormatter(bgBlue),
	"bgPurple":     ansciiFormatter(bgPurple),
	"bgCyan":       ansciiFormatter(bgCyan),
	"bgWhite":      ansciiFormatter(bgWhite),
	"bold":         ansciiFormatter(bold),
	"dim":          ansciiFormatter(dim),
	"italic":       ansciiFormatter(italic),
	"underline":    ansciiFormatter(underline),
	"strike":       ansciiFormatter(strikethrough),
	"ago":          ago,
}

func WithFn(name string, fn any) {
	// TODO: add validation for fn signature via reflect
	colorFns[name] = fn
}

func ansciiFormatter(codes ...string) func(...any) string {
	return func(text ...any) string {
		var result string
		for _, code := range codes {
			result += code
		}
		for _, t := range text {
			switch x := t.(type) {
			case *string:
				if x != nil {
					result += *x
				}
			case []string:
				result += strings.Join(x, ", ")
			case *int:
				if x != nil {
					//nolint:perfsprint // ignore
					result += fmt.Sprint(*x)
				}
			default:
				result += fmt.Sprint(t)
			}
		}
		result += reset
		return result
	}
}
