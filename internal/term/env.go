// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"cmp"
	"github.com/nfx/go-tui/internal/color"
	"io"
	"runtime"
	"slices"
	"strings"
)

// outputEnv is the part of the process environment that decides about color
// and symbols. [defaultIO] reads it once at startup; tests replace it with a
// fixture.
type outputEnv struct {
	forceColor      *string
	noColor         bool
	cliColorForce   bool
	term, colorTerm string
	windowsTerminal bool
	// bgHint is the background estimated from COLORFGBG, the fallback of
	// detection, read with the same lifetime as the color policy.
	bgHint color.Background
	// Ci is set in CI logs, which are rarely color-capable but are viewed in
	// browsers with the Unicode glyphs. A CI job may run on a pseudo-terminal,
	// but nobody answers prompts or watches redraws there, so it is never
	// interactive, see [tio.interactiveWriter] and [hostsTerminal].
	Ci bool
	// locale is the first set of LC_ALL, LC_CTYPE and LANG.
	locale string
	// unicodeHost is set by the terminals that announce themselves and show
	// the Unicode symbols on the legacy Windows console host.
	unicodeHost bool
}

func ReadOutputEnv(lookup func(string) (string, bool)) outputEnv {
	get := func(k string) string { v, _ := lookup(k); return v }
	truthy := func(k string) bool {
		v := strings.ToLower(get(k))
		return v != "" && v != "0" && v != "false"
	}
	cliColorForce := truthy("CLICOLOR_FORCE")
	ci := truthy("CI")
	term := get("TERM")
	e := outputEnv{
		// CI logs are rarely color-capable, unless the pipeline opts in.
		noColor:         get("NO_COLOR") != "" || ci && !cliColorForce,
		cliColorForce:   cliColorForce,
		term:            term,
		colorTerm:       strings.ToLower(get("COLORTERM")),
		windowsTerminal: get("WT_SESSION") != "",
		bgHint:          new(IO).backgroundFromEnv(lookup),
		Ci:              ci,
		locale:          cmp.Or(get("LC_ALL"), get("LC_CTYPE"), get("LANG")),
		unicodeHost: get("TERM_PROGRAM") == "vscode" ||
			get("TERMINAL_EMULATOR") == "JetBrains-JediTerm" ||
			get("ConEmuTask") == "{cmd::Cmder}" ||
			slices.Contains([]string{"xterm-256color", "alacritty", "rxvt-unicode", "rxvt-unicode-256color"}, term),
	}
	// FORCE_COLOR= (empty) is meant to force 16-color output.
	if v, ok := lookup("FORCE_COLOR"); ok {
		e.forceColor = &v
	}
	return e
}

// LegacyConsoles reports whether the default terminal of the platform may be
// the legacy Windows console host, whose fonts lack the Unicode symbols.
var LegacyConsoles = runtime.GOOS == "windows"

// hasUnicode guesses whether the terminal font has the glyphs of the Unicode
// symbols, which no terminal can be asked about. Where the legacy Windows
// console host may be running, only CI logs and the hosts that announce
// themselves show them. Elsewhere, every terminal does but the Linux virtual
// console and one with a locale other than UTF-8.
func (e outputEnv) hasUnicode() bool {
	if !LegacyConsoles {
		return e.term != "linux" && e.utf8Locale()
	}
	return e.Ci || e.windowsTerminal || e.unicodeHost
}

// utf8Locale reports whether the locale, language_TERRITORY.charset@modifier,
// names UTF-8 or no character set, as C and POSIX do.
func (e outputEnv) utf8Locale() bool {
	_, charset, ok := strings.Cut(e.locale, ".")
	charset, _, _ = strings.Cut(charset, "@")
	return !ok || strings.EqualFold(strings.ReplaceAll(charset, "-", ""), "utf8")
}

// ConsoleVT enables VT output processing on a console writer and reports
// whether the console accepts escape sequences. Only Windows consoles need
// explicit enabling, see io_windows.go.
var ConsoleVT = func(io.Writer) bool { return true }

// depth derives color depth from terminal hints, defaulting to ansi16.
func (e outputEnv) depth() color.Profile {
	switch {
	case e.colorTerm == "truecolor" || e.colorTerm == "24bit", e.windowsTerminal:
		return color.ProfileTrueColor
	case strings.Contains(e.term, "256color"):
		return color.ProfileANSI256
	}
	return color.ProfileANSI16
}
