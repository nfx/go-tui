// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/theme"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

// probeTermIO creates a simulated terminal with automatic cleanup.
func probeTermIO(t *testing.T, reply string) (*termIO, *replyingTerminal) {
	t.Helper()
	prev, prevSupported := terminalInputChecker, backgroundProbeSupported
	terminalInputChecker = func(int) bool { return true }
	backgroundProbeSupported = true // the simulated terminal accepts VT input on every platform
	t.Cleanup(func() { terminalInputChecker, backgroundProbeSupported = prev, prevSupported })
	pr, pw := io.Pipe()
	t.Cleanup(func() { assert.NoError(t, pw.Close()) })
	term := &replyingTerminal{reply: reply, in: pw}
	return &termIO{in: &mockDescriptor{Reader: pr, fd: 7}, out: term, fd: 2}, term
}

// routedTio makes tt the current route for cache tests.
func routedTio(tt *termIO) *tio {
	d := &tio{}
	d.setIO(tt.in, tt.out)
	return d
}

func TestDetectBackground_OSCReply(t *testing.T) {
	tt, term := probeTermIO(t, "\x1b]11;rgb:fafa/fafa/fafa\x1b\\")
	d := routedTio(tt)
	b := d.background(t.Context(), tt, color.Background{})
	assert.True(t, b.Measured)
	assert.Equal(t, color.SRGB{250, 250, 250}, b.RGB)
	assert.True(t, b.Light)
	// cached: no second query
	b = d.background(t.Context(), tt, color.Background{})
	assert.True(t, b.Measured)
	assert.Equal(t, 1, term.count())
}

func TestDetectBackground_TimeoutFallsBackOnceToEnv(t *testing.T) {
	tt, term := probeTermIO(t, "") // a terminal that never answers
	hint := color.Background{Light: true}
	d := routedTio(tt)
	b := d.background(t.Context(), tt, hint)
	assert.Equal(t, hint, b)
	d.background(t.Context(), tt, hint)
	assert.Equal(t, 1, term.count()) // the timeout is cached too
}

func TestDetectBackground_MalformedReplyIsNotDark(t *testing.T) {
	tt, _ := probeTermIO(t, "\x1b]11;rgb:zz/00/00\x07")
	b := tt.detectBackground(t.Context(), color.Background{Light: true})
	assert.Equal(t, color.Background{Light: true}, b)
}

func TestDetectBackground_SkipsQuery(t *testing.T) {
	for name, disable := range map[string]func(t *testing.T){
		"no terminal input": func(*testing.T) {
			terminalInputChecker = func(int) bool { return false }
		},
		"unsupported platform": func(*testing.T) {
			backgroundProbeSupported = false // probeTermIO restores the previous value
		},
	} {
		t.Run(name, func(t *testing.T) {
			tt, term := probeTermIO(t, oscReply)
			disable(t)
			assert.Equal(t, color.Background{}, tt.detectBackground(t.Context(), color.Background{}))
			assert.Equal(t, 0, term.count())
		})
	}
}

func TestDetectBackground_ReplacedOutputRouteProbesAgain(t *testing.T) {
	tt, term := probeTermIO(t, oscReply)
	d := routedTio(tt)
	d.background(t.Context(), tt, color.Background{})
	next := &replyingTerminal{reply: oscReply, in: term.in}
	d.setIO(nil, next)
	tt.out = next
	d.background(t.Context(), tt, color.Background{})
	assert.Equal(t, 1, next.count())
}

func TestDetectBackground_KeyTypedDuringProbeIsKept(t *testing.T) {
	tt, _ := probeTermIO(t, oscReply+"a")
	assert.True(t, tt.detectBackground(t.Context(), color.Background{}).Measured)
	assert.Equal(t, 'a', nextEvent(t, tt).key)
}

func TestDetectBackground_KeyBeforeReplyIsKept(t *testing.T) {
	tt, term := probeTermIO(t, oscReply)
	go term.in.Write([]byte("a")) //nolint:errcheck
	time.Sleep(20 * time.Millisecond)
	assert.True(t, tt.detectBackground(t.Context(), color.Background{}).Measured)
	go term.in.Write([]byte("b")) //nolint:errcheck
	assert.Equal(t, 'a', nextEvent(t, tt).key)
	assert.Equal(t, 'b', nextEvent(t, tt).key)
}

func TestDetectBackground_InputBeforeReplyIsKept(t *testing.T) {
	for name, tc := range map[string]struct {
		typed     string
		bracketed bool
		check     func(t *testing.T, ev keyEvent)
	}{
		"coalesced text": {typed: "hello", check: func(t *testing.T, ev keyEvent) {
			t.Helper()
			var more *pasteTextError
			assert.True(t, errors.As(ev.err, &more))
			assert.Equal(t, "hello", string(ev.paste))
		}},
		"bracketed paste": {typed: "\x1b[200~hi\x1b[201~", bracketed: true, check: func(t *testing.T, ev keyEvent) {
			t.Helper()
			assert.NoError(t, ev.err)
			assert.Equal(t, "hi", string(ev.paste))
		}},
		"legacy bracketed paste": {typed: "\x1b[200~hi\x1b[201~", check: func(t *testing.T, ev keyEvent) {
			t.Helper()
			var more *pasteTextError
			assert.True(t, errors.As(ev.err, &more))
			assert.Equal(t, "hi", string(ev.paste))
		}},
		"ctrl-c": {typed: "\x03", check: func(t *testing.T, ev keyEvent) {
			t.Helper()
			assert.ErrorIs(t, ev.err, io.EOF)
		}},
		"ctrl-d": {typed: "\x04", check: func(t *testing.T, ev keyEvent) {
			t.Helper()
			assert.ErrorIs(t, ev.err, io.EOF)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			tt, term := probeTermIO(t, oscReply)
			tt.bracketedPaste = tc.bracketed
			go term.in.Write([]byte(tc.typed)) //nolint:errcheck
			time.Sleep(20 * time.Millisecond)
			tt.detectBackground(t.Context(), color.Background{})
			tc.check(t, nextEvent(t, tt))
		})
	}
}

func TestDetectBackground_RestoredEventsKeepBoundaries(t *testing.T) {
	tt, term := probeTermIO(t, "")
	// A standalone Escape decoded before 'a' arrived must not replay as Alt+a.
	tt.restoreKeys([]keyEvent{{key: keyEscape, readOrder: 1}, {key: 'a', readOrder: 2}})
	go term.in.Write([]byte("b")) //nolint:errcheck
	for _, want := range []keyEvent{{key: keyEscape, readOrder: 1}, {key: 'a', readOrder: 2}} {
		assert.Equal(t, want, nextEvent(t, tt))
	}
	assert.Equal(t, 'b', nextEvent(t, tt).key)
}

func TestDetectBackground_StaleTerminalDoesNotUseCache(t *testing.T) {
	tt, _ := probeTermIO(t, oscReply)
	d := routedTio(tt)
	// Stale terminals must not populate the new route's cache.
	d.setIO(nil, &replyingTerminal{})
	assert.True(t, d.background(t.Context(), tt, color.Background{}).Measured)
	d.mu.RLock()
	defer d.mu.RUnlock()
	assert.True(t, d.bg == nil)
}

func TestParseOSCColor(t *testing.T) {
	for in, want := range map[string]color.SRGB{
		"rgb:0000/0000/0000": {0, 0, 0},
		"rgb:ffff/ffff/ffff": {255, 255, 255},
		"rgb:1e1e/1e1e/2e2e": {0x1e, 0x1e, 0x2e},
		"rgb:f/0/8":          {255, 0, 136}, // one digit scales by 15
		"rgb:ff/80/00":       {255, 128, 0},
		"rgb:fff/800/000":    {255, 128, 0},
	} {
		got, ok := new(termIO).parseOSCColor(in)
		assert.True(t, ok)
		assert.Equal(t, want, got)
	}
	for _, in := range []string{
		"", "?", "rgb:", "rgb:0/0", "rgb:0/0/0/0", "rgb:00000/0/0", "rgb://", "rgb:g/0/0",
		"rgba:0/0/0/0", "#112233", "rgb:-1/0/0", " rgb:0/0/0",
	} {
		_, ok := new(termIO).parseOSCColor(in)
		assert.Equal(t, false, ok)
	}
}

func TestParseColorFGBG(t *testing.T) {
	rgb, ok := new(termIO).parseColorFGBG("15;0")
	assert.True(t, ok)
	assert.Equal(t, color.SRGB{0, 0, 0}, rgb)
	rgb, ok = new(termIO).parseColorFGBG("0;default;15")
	assert.True(t, ok)
	assert.Equal(t, color.SRGB{255, 255, 255}, rgb)
	for _, in := range []string{"", "15", "15;default", "15;", "15;256", "15;-1", "a;b", "1;2;3;4"} {
		_, ok := new(termIO).parseColorFGBG(in)
		assert.Equal(t, false, ok)
	}
}

func TestBackgroundFromEnv(t *testing.T) {
	env := func(v string) func(string) (string, bool) {
		return func(k string) (string, bool) { return v, k == "COLORFGBG" }
	}
	b := new(termIO).backgroundFromEnv(env("0;15"))
	assert.True(t, b.Light)
	assert.Equal(t, false, b.Measured)
	// Blue is dark; mid gray is light despite its low palette index.
	assert.Equal(t, false, new(termIO).backgroundFromEnv(env("15;4")).Light)
	assert.True(t, new(termIO).backgroundFromEnv(env("15;8")).Light)
	assert.Equal(t, color.Background{}, new(termIO).backgroundFromEnv(env("default;default")))
	assert.Equal(t, color.Background{}, new(termIO).backgroundFromEnv(nil))
}

func TestDetectBackground_ConcurrentCallersQueryOnce(t *testing.T) {
	tt, term := probeTermIO(t, oscReply)
	d := routedTio(tt)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.background(t.Context(), tt, color.Background{})
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, term.count())
}

func TestInstalledThemeFallsBackToASCIISymbols(t *testing.T) {
	th := theme.New()
	th.Symbols.Focus = "→"
	d := &tio{writer: &bytes.Buffer{}}
	d.env = readOutputEnv(func(k string) (string, bool) {
		if k == "TERM" {
			return "linux", true
		}
		return "", false
	})
	assert.NoError(t, d.installTheme(th))
	sym := d.currentTheme().Source.Symbols
	assert.Equal(t, "?", sym.Prompt)
	assert.Equal(t, "|", sym.Guide)
	assert.Equal(t, "→", sym.Focus) // chosen by the theme
	assert.Equal(t, theme.New().Symbols.ASCII().Spinner, sym.Spinner)
	assert.Equal(t, "◆", th.Symbols.Prompt) // the caller's copy is unchanged

	d.env = readOutputEnv(func(string) (string, bool) { return "", false })
	assert.NoError(t, d.installTheme(th))
	assert.Equal(t, "◆", d.currentTheme().Source.Symbols.Prompt)
}

func TestBindThemeDetectsOnlyWhenColorsDependOnIt(t *testing.T) {
	light := theme.New()
	light.Light = true
	for _, tc := range []struct {
		name    string
		env     string
		theme   theme.Theme
		queries int
	}{
		{"truecolor probes", "FORCE_COLOR=3", theme.New(), 1},
		{"forced light skips", "FORCE_COLOR=3", light, 0},
		{"plain skips", "FORCE_COLOR=0", theme.New(), 0},
		{"attrs skip", "NO_COLOR=1", theme.New(), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tt, term := probeTermIO(t, "\x1b]11;rgb:fafa/fafa/fafa\x1b\\")
			d := routedTio(tt)
			d.env = readOutputEnv(func(k string) (string, bool) {
				name, v, _ := strings.Cut(tc.env, "=")
				return v, k == name
			})
			c, err := render.Compile(tc.theme)
			assert.NoError(t, err)
			d.bindTheme(t.Context(), c, tt.out, tt)
			assert.Equal(t, tc.queries, term.count())
		})
	}
}

func TestBindThemeUsesMeasuredOrEstimatedBackground(t *testing.T) {
	c, err := render.Compile(theme.New())
	assert.NoError(t, err)
	tt, _ := probeTermIO(t, "\x1b]11;rgb:fafa/fafa/fafa\x1b\\")
	d := routedTio(tt)
	d.env = readOutputEnv(func(k string) (string, bool) { return "3", k == "FORCE_COLOR" })
	measured := d.bindTheme(t.Context(), c, tt.out, tt)
	want := c.Roles(color.ProfileTrueColor, c.Palette.Resolve(false, color.SRGB{250, 250, 250}, true))
	assert.Equal(t, want, measured.Roles)

	// without a terminal lease, the environment estimates a light background
	d.env = readOutputEnv(func(k string) (string, bool) {
		return map[string]string{"FORCE_COLOR": "3", "COLORFGBG": "0;15"}[k], k == "FORCE_COLOR" || k == "COLORFGBG"
	})
	estimated := d.bindTheme(t.Context(), c, &bytes.Buffer{}, nil)
	want = c.Roles(color.ProfileTrueColor, c.Palette.Resolve(true, color.SRGB{}, false))
	assert.Equal(t, want, estimated.Roles)
	assert.True(t, estimated.Roles.Foreground == nil) // estimated, not measured
}

func TestBindThemeReusesRouteDetectionWithoutWaiting(t *testing.T) {
	c, err := render.Compile(theme.New())
	assert.NoError(t, err)
	tt, term := probeTermIO(t, "\x1b]11;rgb:fafa/fafa/fafa\x1b\\")
	d := routedTio(tt)
	d.env = readOutputEnv(func(k string) (string, bool) { return "3", k == "FORCE_COLOR" })
	estimated := d.bindTheme(t.Context(), c, tt.out, nil) // nothing detected yet
	measured := d.bindTheme(t.Context(), c, tt.out, tt)
	assert.True(t, estimated.Roles.Foreground == nil)
	assert.True(t, measured.Roles.Foreground != nil)

	// output-only work drawing on the route reuses its detection
	for _, out := range []io.Writer{tt.out, &terminalStderr{parent: d}} {
		assert.Equal(t, measured.Roles, d.bindTheme(t.Context(), c, out, nil).Roles)
	}
	assert.Equal(t, 1, term.count())
	// other destinations estimate, and so does another tio's Stderr
	assert.Equal(t, estimated.Roles, d.bindTheme(t.Context(), c, &bytes.Buffer{}, nil).Roles)
	assert.Equal(t, estimated.Roles, d.bindTheme(t.Context(), c, &terminalStderr{parent: &tio{}}, nil).Roles)

	// a probe in progress does not hold up output-only work
	d.bgMu.Lock()
	bound := make(chan render.Binding)
	go func() { bound <- d.bindTheme(t.Context(), c, tt.out, nil) }()
	select {
	case b := <-bound:
		assert.Equal(t, measured.Roles, b.Roles)
	case <-time.After(time.Second):
		t.Fatal("output-only binding waited for a probe")
	}
	d.bgMu.Unlock()

	// replacing the route forgets the detection
	d.setIO(tt.in, tt.out)
	assert.Equal(t, estimated.Roles, d.bindTheme(t.Context(), c, tt.out, nil).Roles)
}
