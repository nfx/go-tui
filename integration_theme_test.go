// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/theme"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/assert"
)

// themedProfiles are the five output profiles, from the environment.
var themedProfiles = []struct{ name, env string }{
	{"plain", "FORCE_COLOR=0"},
	{"attrs", "NO_COLOR=1"},
	{"ansi16", "FORCE_COLOR=1"},
	{"ansi256", "FORCE_COLOR=2"},
	{"truecolor", "FORCE_COLOR=3"},
}

var sgr = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// checkProfile fails unless every SGR in out is one that profile allows, and
// a colored profile used its own encoding at least once.
func checkProfile(t *testing.T, profile, out string) {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range sgr.FindAllStringSubmatch(out, -1) {
		params := strings.Split(m[1], ";")
		for i := 0; i < len(params); i++ {
			p := params[i]
			kind := "attr"
			switch {
			case p == "38" || p == "48":
				kind = params[i] + ";" + params[i+1]
				if params[i+1] == "5" {
					i += 2
				} else {
					i += 4
				}
			case len(p) == 2 && (p[0] == '3' || p[0] == '4' || p[0] == '9') && p[1] <= '7',
				len(p) == 3 && p[:2] == "10":
				kind = "ansi16"
			case p == "39" || p == "49":
				kind = "default"
			}
			seen[kind] = true
		}
	}
	allowed := map[string][]string{
		"plain":     {},
		"attrs":     {"attr"},
		"ansi16":    {"attr", "default", "ansi16"},
		"ansi256":   {"attr", "default", "ansi16", "38;5", "48;5"},
		"truecolor": {"attr", "default", "38;2", "48;2"},
	}[profile]
	for kind := range seen {
		if !slices.Contains(allowed, kind) {
			t.Errorf("%s output has %s SGR", profile, kind)
		}
	}
	want := map[string]string{"attrs": "attr", "ansi16": "ansi16", "ansi256": "38;5", "truecolor": "38;2"}[profile]
	if want != "" && !seen[want] {
		t.Errorf("%s output has no %s SGR", profile, want)
	}
}

// themedLayout is what a themed session shows with one choice of layout
// and symbols.
type themedLayout struct {
	inline, ascii bool
	spinner       string   // the running spinner row
	progress      []string // the start of the progress rows
	input         []string // Name while typing x
	dropdown      []string // Pick with b focused
	checklist     []string // Tags with x checked
	answers       []string // retained rows of the answered prompts
}

var themedLayouts = []themedLayout{{
	spinner:   "~ fetch: pages",
	progress:  []string{"Download  25%  ━...", "1 / 4 items · ..."},
	input:     []string{"◆ Name", "│ x"},
	dropdown:  []string{"◆ Pick", "│   a", "│ ❯ b", "│ ↑↓ move · type filter"},
	checklist: []string{"◆ Tags", "│ ❯ ◉ x", "│   ○ y", "│ ↑↓ move · space toggle · type filter"},
	answers:   []string{"✔ Name · x", "✔ Pick · b", "✔ Tags · x", "✔ Go · Yes", "✔ Count · 3"},
}, {
	ascii:     true,
	spinner:   "~ fetch: pages",
	progress:  []string{"Download  25%  =...", "1 / 4 items | ..."},
	input:     []string{"? Name", "| x"},
	dropdown:  []string{"? Pick", "|   a", "| > b", "| ^v move · type filter"},
	checklist: []string{"? Tags", "| > x x", "|   o y", "| ^v move · space toggle · type filter"},
	answers:   []string{"+ Name · x", "+ Pick · b", "+ Tags · x", "+ Go · Yes", "+ Count · 3"},
}, {
	inline:    true,
	spinner:   "~ fetch: pages",
	progress:  []string{"Download  25%  ━...", "1 / 4 items · ..."},
	input:     []string{"◆ Name x"},
	dropdown:  []string{"◆ Pick   a", "❯ b", "↑↓ move · type filter"},
	checklist: []string{"◆ Tags", "❯ ◉ x", "○ y", "↑↓ move · space toggle · type filter"},
	answers:   []string{"✔ Name · x", "✔ Pick · b", "✔ Tags · x", "✔ Go · Yes", "✔ Count · 3"},
}, {
	inline:    true,
	ascii:     true,
	spinner:   "~ fetch: pages",
	progress:  []string{"Download  25%  =...", "1 / 4 items | ..."},
	input:     []string{"? Name x"},
	dropdown:  []string{"? Pick   a", "> b", "^v move · type filter"},
	checklist: []string{"? Tags", "> x x", "o y", "^v move · space toggle · type filter"},
	answers:   []string{"+ Name · x", "+ Pick · b", "+ Tags · x", "+ Go · Yes", "+ Count · 3"},
}}

func (l themedLayout) theme() theme.Theme {
	th := theme.New()
	th.Inline = l.inline
	if l.ascii {
		th.Symbols = tui.ASCIISymbols
	}
	th.Symbols.Spinner = []string{"~"} // one frame, so every redraw looks alike
	return th
}

func (l themedLayout) String() string {
	name := map[bool]string{false: "guided", true: "inline"}[l.inline]
	return name + "/" + map[bool]string{false: "unicode", true: "ascii"}[l.ascii]
}

func TestThemedWidgetsShareTerminal(t *testing.T) {
	for _, l := range themedLayouts {
		for _, profile := range themedProfiles {
			t.Run(l.String()+"/"+profile.name, func(t *testing.T) {
				tui.WithEnv(t, profile.env)
				term := newTerminal(t, 30)
				tui.UseTheme(t, l.theme())
				themedSession(t, term, l)
				checkProfile(t, profile.name, term.received())
			})
		}
	}
}

// themedSession runs prompts, activity, logs and static output together,
// checking the screen at every step.
func themedSession(t *testing.T, term *terminal, l themedLayout) {
	t.Helper()
	group, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	spinner, err := group.Add(t.Context(), tui.WithPrefixf("fetch"))
	assert.NoError(t, err)
	spinner.Update("pages")
	term.waitRows(t, l.spinner)
	bar, err := tui.NewMaxProgressBar(t.Context(), "Download", 4)
	assert.NoError(t, err)
	bar.Add(1)
	term.waitRows(t, append([]string{l.spinner}, l.progress...)...)
	_, err = fmt.Fprintln(tui.Stderr(), "log line")
	assert.NoError(t, err)
	live := append([]string{"log line", l.spinner}, l.progress...)
	term.waitRows(t, live...)

	var retained []string
	rows := func(frame ...string) []string {
		return append(append(append(append([]string{"log line"}, retained...), l.spinner), l.progress...), frame...)
	}
	res := prompt(func() (string, error) { return tui.Input(t.Context(), "Name") })
	term.press(t, "x")
	term.waitRows(t, rows(l.input...)...)
	term.press(t, keyEnter)
	assert.Equal(t, "x", answer(t, res))
	retained = l.answers[:1]
	term.waitRows(t, rows()...)

	res = prompt(func() (string, error) { return tui.Dropdown(t.Context(), "Pick", []string{"a", "b"}) })
	term.press(t, keyDown)
	term.waitRows(t, rows(l.dropdown...)...)
	term.press(t, keyEnter)
	assert.Equal(t, "b", answer(t, res))
	retained = l.answers[:2]
	term.waitRows(t, rows()...)

	res = prompt(func() (string, error) {
		v, err := tui.Multichoice(t.Context(), "Tags", []string{"x", "y"})
		return fmt.Sprint(v), err
	})
	term.press(t, " ")
	term.waitRows(t, rows(l.checklist...)...)
	term.press(t, keyEnter)
	assert.Equal(t, "[x]", answer(t, res))
	retained = l.answers[:3]
	term.waitRows(t, rows()...)

	res = prompt(func() (string, error) { return strconv.FormatBool(tui.Confirm(t.Context(), "Go")), nil })
	term.press(t, keyEnter)
	assert.Equal(t, "true", answer(t, res))
	res = prompt(func() (string, error) {
		v, err := tui.Number[int](t.Context(), "Count")
		return strconv.Itoa(v), err
	})
	term.press(t, "3")
	term.press(t, keyEnter)
	assert.Equal(t, "3", answer(t, res))
	res = prompt(func() (string, error) { return tui.Password(t.Context(), "Secret") })
	term.press(t, "s")
	term.press(t, keyEnter)
	assert.Equal(t, "s", answer(t, res))
	retained = l.answers // a password retains nothing
	term.waitRows(t, rows()...)

	// static output goes above the activity, like logs
	type file struct {
		Name string
		Size int
	}
	assert.NoError(t, tui.TableAuto(tui.Stderr(), []file{{"a", 1}, {"bb", 22}}))
	assert.NoError(t, tui.Facts(tui.Stderr(), file{"c", 3}))
	assert.NoError(t, tui.PrettyJSON(tui.Stderr(), map[string]any{"k": []int{1}}))
	retained = append(append([]string{}, l.answers...),
		"NAME   SIZE", "a         1", "bb       22",
		"NAME   SIZE", "c         3",
		"{", `"k": [`, "1", "]", "}")
	term.waitRows(t, rows()...)

	// finished activity leaves nothing behind
	assert.NoError(t, spinner.Close())
	bar.Add(3)
	assert.NoError(t, bar.Close())
	group.Close()
	term.waitRows(t, append([]string{"log line"}, retained...)...)
}

const (
	backgroundQuery = "\x1b]11;?\a"
	whiteBackground = "\x1b]11;rgb:ffff/ffff/ffff\x1b\\"
	blackBackground = "\x1b]11;rgb:0000/0000/0000\x1b\\"
)

var foreground = regexp.MustCompile(`\x1b\[38;2;[0-9;]+m`)

// textColor is the truecolor SGR right before text in out, or "".
func textColor(out, text string) string {
	m := regexp.MustCompile(`(\x1b\[38;2;[0-9;]+m)` + regexp.QuoteMeta(text)).FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestThemedRouteSharesDetection(t *testing.T) {
	tui.WithEnv(t, "FORCE_COLOR=3")
	tui.ProbingKeyboard(t, keyboardFd)
	term := newTerminal(t, 20)
	tui.UseTheme(t, l0.theme())

	// activity started before the first prompt estimates the background
	early, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	defer early.Close()
	_, err = early.Add(t.Context(), tui.WithPrefixf("early"))
	assert.NoError(t, err)
	term.waitRows(t, "~ early:")

	res := prompt(func() (string, error) { return tui.Input(t.Context(), "Name") })
	term.answerProbe(t, 1, whiteBackground)
	term.press(t, "x")
	term.press(t, keyEnter)
	assert.Equal(t, "x", answer(t, res))
	term.waitRows(t, "✔ Name · x", "~ early:")
	ink := textColor(term.received(), "x")
	assert.True(t, ink != "") // measured: the draft is drawn in Foreground

	// later work on the same route reuses the measurement without a query
	late, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	defer late.Close()
	_, err = late.Add(t.Context(), tui.WithPrefixf("late"))
	assert.NoError(t, err)
	assert.NoError(t, tui.TableAuto(tui.Stderr(), []struct{ Name string }{{"cell"}}))
	assert.NoError(t, tui.PrettyJSON(tui.Stderr(), map[string]string{"k": "json"}))
	res = prompt(func() (string, error) { return tui.Dropdown(t.Context(), "Pick", []string{"item"}) })
	term.press(t, keyEnter)
	assert.Equal(t, "item", answer(t, res))
	term.waitRows(t, "✔ Name · x", "NAME", "cell", "{", `"k": "json"`, "}", "✔ Pick · item", "~ early:", "~ late:")
	out := term.received()
	assert.Equal(t, 1, strings.Count(out, backgroundQuery))
	for _, text := range []string{"late", "cell", `"json"`, "item"} {
		assert.Equal(t, ink, textColor(out, text))
	}
	// the measurement does not recolor activity bound before it
	assert.Equal(t, "", textColor(out, "early"))
	// nor reach a destination that is not the terminal
	var buf strings.Builder
	assert.NoError(t, tui.TableAuto(&buf, []struct{ Name string }{{"cell"}}))
	assert.Equal(t, "", textColor(buf.String(), "cell"))
	early.Close()
	late.Close()

	// a new route detects its own background
	next := newTerminal(t, 20)
	res = prompt(func() (string, error) { return tui.Input(t.Context(), "Name") })
	next.answerProbe(t, 1, blackBackground)
	next.press(t, "y")
	next.press(t, keyEnter)
	assert.Equal(t, "y", answer(t, res))
	assert.NoError(t, tui.TableAuto(tui.Stderr(), []struct{ Name string }{{"cell"}}))
	next.waitRows(t, "✔ Name · y", "NAME", "cell")
	out = next.received()
	dark := textColor(out, "y")
	assert.True(t, dark != "" && dark != ink)
	assert.Equal(t, dark, textColor(out, "cell"))
}

func TestThemedExplicitAppearanceNeverProbes(t *testing.T) {
	tui.WithEnv(t, "FORCE_COLOR=3")
	tui.ProbingKeyboard(t, keyboardFd)
	term := newTerminal(t, 20)
	th := l0.theme()
	th.Light = true
	tui.UseTheme(t, th)
	res := prompt(func() (string, error) { return tui.Input(t.Context(), "Name") })
	term.press(t, "x") // the first read is a key, not a reply
	term.press(t, keyEnter)
	assert.Equal(t, "x", answer(t, res))
	assert.NoError(t, tui.TableAuto(tui.Stderr(), []struct{ Name string }{{"cell"}}))
	term.waitRows(t, "✔ Name · x", "NAME", "cell")
	out := term.received()
	assert.Equal(t, 0, strings.Count(out, backgroundQuery))
	// the light variant draws neutrals without a measurement
	ink := textColor(out, "x")
	assert.True(t, ink != "")
	assert.Equal(t, ink, textColor(out, "cell"))
}

// l0 is the Guided Unicode layout.
var l0 = themedLayouts[0]

// probingSession is a terminal answering background queries, with a spinner
// drawing while the prompts read.
func probingSession(t *testing.T) (*terminal, *tui.Spinners) {
	t.Helper()
	tui.WithEnv(t, "FORCE_COLOR=3")
	tui.ProbingKeyboard(t, keyboardFd)
	term := newTerminal(t, 20)
	tui.UseTheme(t, l0.theme())
	group, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	t.Cleanup(group.Close)
	_, err = group.Add(t.Context(), tui.WithPrefixf("work"))
	assert.NoError(t, err)
	term.waitRows(t, "~ work:")
	return term, group
}

func TestThemedProbeTimeoutReplaysKeys(t *testing.T) {
	term, group := probingSession(t)
	res := prompt(func() (string, error) { return tui.Input(t.Context(), "Name") })
	read := term.blockedRead(t)
	term.waitQueries(t, 1)
	read <- "a" // typed while the terminal does not answer
	term.waitRows(t, "~ work:", "◆ Name", "│ a")
	// a reply after the timeout is not typed text
	term.press(t, whiteBackground)
	term.press(t, "b")
	term.waitRows(t, "~ work:", "◆ Name", "│ ab")
	term.press(t, keyEnter)
	assert.Equal(t, "ab", answer(t, res))

	// the timeout is remembered for the route: the next prompt reads keys at once
	res = prompt(func() (string, error) { return tui.Input(t.Context(), "Next") })
	term.press(t, "c")
	term.press(t, keyEnter)
	assert.Equal(t, "c", answer(t, res))
	group.Close()
	term.waitRows(t, "✔ Name · ab", "✔ Next · c")
	out := term.received()
	assert.Equal(t, 1, strings.Count(out, backgroundQuery))
	// the unknown background keeps neutrals in the terminal default
	assert.Equal(t, "", textColor(out, "ab"))
}

func TestThemedProbeCancellationIsNotRemembered(t *testing.T) {
	term, group := probingSession(t)
	ctx, cancel := context.WithCancel(t.Context())
	res := prompt(func() (string, error) { return tui.Input(ctx, "Name") })
	read := term.blockedRead(t)
	term.waitQueries(t, 1)
	cancel()
	select {
	case r := <-res:
		assert.ErrorIs(t, r.err, context.Canceled)
	case <-time.After(failsafe):
		t.Fatal("cancel blocked")
	}
	term.waitRows(t, "~ work:")
	// a key typed after the cancellation stays with the next prompt, which
	// asks again because a cancelled probe is not remembered
	read <- "z"
	res = prompt(func() (string, error) { return tui.Input(t.Context(), "Next") })
	term.answerProbe(t, 2, whiteBackground)
	term.waitRows(t, "~ work:", "◆ Next", "│ z")
	term.press(t, keyEnter)
	assert.Equal(t, "z", answer(t, res))
	group.Close()
	term.waitRows(t, "✔ Next · z")
	out := term.received()
	assert.Equal(t, 2, strings.Count(out, backgroundQuery))
	assert.True(t, textColor(out, "z") != "")
}

// failingWriter is a destination that rejects every write.
type failingWriter struct{}

var errRejected = errors.New("rejected")

func (failingWriter) Write([]byte) (int, error) { return 0, errRejected }

func TestThemedFailuresStayWithTheirOwner(t *testing.T) {
	tui.WithEnv(t, "FORCE_COLOR=1")
	term := newTerminal(t, 20)
	th := l0.theme()
	errBoom := errors.New("boom")
	// check fails on "fail", so that one widget breaks while others draw
	th.Funcs = map[string]any{"check": func(s string) (string, error) {
		if s == "fail" {
			return "", errBoom
		}
		return s, nil
	}}
	th.Templates.Prompt.Label = `{{ accent (symbol "Prompt") }} {{ bold (text (check .Label)) }}`
	th.Templates.Spinner.Running = `{{ accent .Frame }} {{ text (check .Message) }}`
	th.Templates.Table.Cell = `{{ text (check .Value) }}`
	tui.UseTheme(t, th)

	group, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	defer group.Close()
	work, err := group.Add(t.Context())
	assert.NoError(t, err)
	work.Update("work")
	bar, err := tui.NewMaxProgressBar(t.Context(), "Download", 4)
	assert.NoError(t, err)
	bar.Add(1)
	live := []string{"~ work", "Download  25%  ━...", "1 / 4 items..."}
	term.waitRows(t, live...)

	// a prompt whose helper fails returns the error and leaves the screen
	_, err = tui.Input(t.Context(), "fail")
	assert.ErrorIs(t, err, errBoom)
	term.waitRows(t, live...)
	res := prompt(func() (string, error) { return tui.Input(t.Context(), "Name") })
	term.press(t, "x")
	term.press(t, keyEnter)
	assert.Equal(t, "x", answer(t, res))
	term.waitRows(t, append([]string{"✔ Name · x"}, live...)...)

	// static output with a failing cell writes nothing
	err = tui.TableAuto(tui.Stderr(), []struct{ Name string }{{"ok"}, {"fail"}})
	assert.ErrorIs(t, err, errBoom)
	// a failing destination fails only its own output
	err = tui.TableAuto(failingWriter{}, []struct{ Name string }{{"ok"}})
	assert.ErrorIs(t, err, errRejected)
	err = tui.PrettyJSON(failingWriter{}, map[string]int{"k": 1})
	assert.ErrorIs(t, err, errRejected)

	// a spinner group whose row fails ends alone, clearing its rows
	other, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	defer other.Close()
	broken, err := other.Add(t.Context())
	assert.NoError(t, err)
	broken.Update("broken")
	term.waitRows(t, "✔ Name · x", "~ work", "Download  25%  ━...", "1 / 4 items...", "~ broken")
	broken.Update("fail")
	term.waitRows(t, append([]string{"✔ Name · x"}, live...)...)
	assert.ErrorIs(t, broken.Close(), errBoom)
	_, err = other.Add(t.Context())
	assert.ErrorIs(t, err, errBoom)

	// the survivors still work
	work.Update("done")
	bar.Add(3)
	term.waitRows(t, "✔ Name · x", "~ done")
	assert.NoError(t, bar.Close())
	assert.NoError(t, work.Close())
	group.Close()
	term.waitRows(t, "✔ Name · x")
}

func TestThemedRunningWidgetsKeepTheirSnapshot(t *testing.T) {
	tui.WithEnv(t, "FORCE_COLOR=0")
	term := newTerminal(t, 20)
	old := themedLayouts[1].theme() // Guided ASCII
	tui.UseTheme(t, old)
	group, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	defer group.Close()
	first, err := group.Add(t.Context(), tui.WithPrefixf("first"))
	assert.NoError(t, err)
	bar, err := tui.NewMaxProgressBar(t.Context(), "Download", 4)
	assert.NoError(t, err)
	bar.Add(1)
	res := prompt(func() (string, error) { return tui.Dropdown(t.Context(), "Pick", []string{"a", "b"}) })
	picking := []string{"? Pick", "| > a", "|   b", "| ^v move · type filter"}
	term.waitRows(t, append([]string{"~ first:", "Download  25%  =...", "1 / 4 items..."}, picking...)...)

	next := themedLayouts[0].theme() // Guided Unicode
	next.Symbols.Spinner = []string{"*"}
	assert.NoError(t, tui.SetDefaultTheme(next))

	// redraws of running widgets, and a spinner added to a running group,
	// keep the theme they started with
	_, err = group.Add(t.Context(), tui.WithPrefixf("second"))
	assert.NoError(t, err)
	bar.Add(1)
	term.press(t, keyDown)
	running := []string{"~ first:", "~ second:", "Download  50%  =...", "2 / 4 items..."}
	term.waitRows(t, append(running, "? Pick", "|   a", "| > b", "| ^v move · type filter")...)
	term.press(t, keyEnter)
	assert.Equal(t, "b", answer(t, res))

	// new operations take the installed theme
	fresh, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	defer fresh.Close()
	_, err = fresh.Add(t.Context(), tui.WithPrefixf("third"))
	assert.NoError(t, err)
	res = prompt(func() (string, error) { return tui.Input(t.Context(), "Name") })
	term.press(t, "x")
	term.waitRows(t, append(append([]string{"+ Pick · b"}, running...), "* third:", "◆ Name", "│ x")...)
	term.press(t, keyEnter)
	assert.Equal(t, "x", answer(t, res))
	assert.NoError(t, first.Close())
	bar.Add(2)
	assert.NoError(t, bar.Close())
	group.Close()
	fresh.Close()
	term.waitRows(t, "+ Pick · b", "✔ Name · x")
}

func TestThemedConcurrentInstallationAndRendering(t *testing.T) {
	tui.WithEnv(t, "FORCE_COLOR=0")
	term := newTerminal(t, 20)
	themes := map[string]theme.Theme{}
	for name, gap := range map[string]int{"A": 2, "B": 5} {
		th := l0.theme()
		th.Templates.Prompt.Label = theme.Template(name + " {{ .Label }}")
		th.Templates.Prompt.Answer = theme.Template(name + " {{ .Label }} · {{ .Value }}")
		th.Templates.Table.Header = theme.Template(name + "{{ .Header }}")
		th.Table.ColumnGap = gap
		themes[name] = th
	}
	type row struct{ Name, Kind string }
	table := func() string {
		var buf strings.Builder
		assert.NoError(t, tui.TableAuto(&buf, []row{{"a", "b"}}))
		return buf.String()
	}
	want := map[string]bool{}
	for _, name := range []string{"B", "A"} {
		tui.UseTheme(t, themes[name])
		want[table()] = true
	}
	assert.Equal(t, 2, len(want))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { // installs alternate, with helper registrations in between
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			assert.NoError(t, tui.SetDefaultTheme(themes[[]string{"A", "B"}[i%2]]))
			tui.WithFn("shout", strings.ToUpper)
		}
	}()
	tables := make(chan string, 1000)
	go func() { // static output renders while themes change
		defer wg.Done()
		defer close(tables)
		for {
			// a full buffer must not keep the producer from seeing stop
			select {
			case <-stop:
				return
			case tables <- table():
			}
		}
	}()
	go func() { // so does activity
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			group, err := tui.NewSpinners(t.Context())
			assert.NoError(t, err)
			_, err = group.Add(t.Context(), tui.WithPrefixf("spin"))
			assert.NoError(t, err)
			group.Close()
		}
	}()

	const prompts = 10
	for i := range prompts {
		res := prompt(func() (string, error) { return tui.Input(t.Context(), fmt.Sprintf("p%d", i)) })
		term.press(t, "x")
		term.press(t, keyEnter)
		assert.Equal(t, "x", answer(t, res))
	}
	close(stop)
	wg.Wait()

	// every table comes from one theme: its header prefix and its gap agree
	n := 0
	for out := range tables {
		n++
		if !want[out] {
			t.Fatalf("mixed table %q", out)
		}
	}
	assert.True(t, n > 0)
	// every prompt answers in the theme its label was drawn with
	syncer, ok := tui.Stderr().(interface{ Sync() error })
	assert.True(t, ok)
	assert.NoError(t, syncer.Sync())
	out := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`).ReplaceAllString(term.received(), "")
	for i := range prompts {
		label := regexp.MustCompile(fmt.Sprintf(`([AB]) p%d\b`, i)).FindAllStringSubmatch(out, -1)
		// the answer, and the label unless the arbiter merged its frame away
		assert.True(t, len(label) >= 1)
		for _, m := range label {
			assert.Equal(t, label[0][1], m[1])
		}
	}
}
