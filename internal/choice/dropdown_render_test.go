// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestDropdownRenderInitAndItems(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"alpha", "beta", "gamma"}
	d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
		return terst.NewTermIO(20, 6), nil
	}
	io := terst.NewTermIO(20, 6)

	assert.NoError(t, d.parseTemplates())
	longest, err := d.renderInit(io)
	assert.NoError(t, err)
	assert.True(t, longest > 0)
	assert.True(t, len(d.displayed) > 0)

	assert.True(t, len(d.active[d.displayed[0]]) > 0)
}

func TestDropdownRenderInitEmitsEvents(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"alpha", "beta"}
	assert.NoError(t, d.parseTemplates())
	io := terst.NewTermIO(20, 6)
	var events []dropdownOutputEvent
	d.eventSink = func(ev dropdownOutputEvent) {
		events = append(events, ev)
	}
	_, err := d.renderInit(io)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(events))
	init, ok := events[0].(dropdownInit)
	assert.True(t, ok)
	assert.Equal(t, "Select from list", init.Label)
	_, ok = events[1].(dropdownAppendItem)
	assert.True(t, ok)
	_, ok = events[2].(dropdownAppendItem)
	assert.True(t, ok)
}

// bindDropdown binds templates and prompt chrome for tests that bypass run.
func bindDropdown(t *testing.T, d *dropdown) {
	t.Helper()
	terst.WithEnv(t)
	if d.out == nil {
		d.out = &bytes.Buffer{}
	}
	tt := &term.IO{Out: d.out, Width: 40, Height: 10}
	assert.NoError(t, d.parseTemplates())
	assert.NoError(t, d.bind(term.Default.BindTheme(t.Context(), d.theme, d.out, tt)))
	assert.NoError(t, d.renderChrome())
}

func TestDropdownRenderRemembersLongestWidth(t *testing.T) {
	d := newDropdown(t.Context())
	d.Items = []any{"short", "a considerably longer item"}
	bindDropdown(t, d)
	tio := terst.NewTermIO(40, 6)
	first, err := d.renderInit(tio)
	assert.NoError(t, err)
	second, err := d.renderInit(tio)
	assert.NoError(t, err)
	assert.True(t, first > 20)
	assert.Equal(t, first, second)

	_, err = d.renderInit(tio)
	assert.NoError(t, err)
	tio.Width = 30
	var buf bytes.Buffer
	assert.NoError(t, d.render(tio, &buf))
	_ = d.addItem(6, "x") //nolint:errcheck // test cleanup
	assert.Equal(t, first, d.longest)
}

// themedDropdownOpt routes a themed dropdown through a scripted terminal.
func themedDropdownOpt(
	t *testing.T,
	th theme.Theme,
	width int,
	keys ...string,
) (o option.Opt, out *bytes.Buffer, restored *bool) {
	t.Helper()
	s := scripted(t, th, width, keys...)
	return option.OpT(func(d *dropdown) error {
		d.in, d.out, d.makeTermIO = s.in, s.out, s.makeTermIO
		return nil
	}), s.out, s.restored
}

// boundDropdown is a themed dropdown over items, parsed and bound to a
// terminal of the given size, which render draws a frame on.
func boundDropdown(
	t *testing.T,
	th theme.Theme,
	width, height int,
	items []any,
	o ...option.Opt,
) (*dropdown, *term.IO) {
	t.Helper()
	terst.UseTheme(t, th)
	d := newDropdown(t.Context())
	d.Label, d.Items = "Fruit", items
	assert.NoError(t, option.Opts(o).Apply(d))
	assert.NoError(t, d.parseTemplates())
	tt := &term.IO{Out: &bytes.Buffer{}, Width: width, Height: height}
	assert.NoError(t, d.bind(term.Default.BindTheme(t.Context(), d.theme, d.out, tt)))
	assert.NoError(t, d.renderChrome())
	return d, tt
}

// frame draws one frame of d on tt.
func frame(t *testing.T, d *dropdown, tt *term.IO) string {
	t.Helper()
	var buf bytes.Buffer
	assert.NoError(t, d.render(tt, &buf))
	return buf.String()
}

var fruits = []string{"apple", "banana", "cherry"}

func TestThemedDropdownGuidedFrameAndAnswer(t *testing.T) {
	o, out, restored := themedDropdownOpt(t, terst.ASCIITheme(false), 30, "\x1b[B", "\r")
	got, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, "banana", got)
	assert.True(t, *restored)
	erase := "\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"
	assert.Equal(t, ""+
		// the label has its own row, the rows and the hint are behind the guide
		"\r? Fruit\n\r| > apple\n\r|   banana\n\r|   cherry\n\r| ^v move · type filter\n\r"+
		// the next frame erases all five rows first
		erase+
		"\r? Fruit\n\r|   apple\n\r| > banana\n\r|   cherry\n\r| ^v move · type filter\n\r"+
		// confirming erases the frame once, leaving the rows above it alone
		erase+
		"+ Fruit · banana\n", out.String())
}

func TestThemedDropdownInlineIndentsRowsUnderLabel(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, terst.ASCIITheme(true), 30, "\x1b[B", "\r")
	_, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(out.String(), ""+
		"\r? Fruit > apple\n\r          banana\n\r          cherry\n\r^v move · type filter\n\r"))
	assert.Contains(t, out.String(), "\r? Fruit   apple\n\r        > banana\n")
}

func TestThemedDropdownInlineLabelTooWideTakesItsRow(t *testing.T) {
	d, tt := boundDropdown(t, terst.ASCIITheme(true), 12, 10, []any{"apple", "banana"})
	assert.Equal(t, "\r? Fruit\n\r> apple\n\r  banana\n\r^v move · …\n\r", frame(t, d, tt))
}

func TestThemedDropdownMoreRowAndShortTerminals(t *testing.T) {
	items := []any{"a", "b", "c", "d", "e"}
	for _, tc := range []struct {
		height int
		want   string
	}{
		{10, "\r? Fruit\n\r| > a\n\r|   b\n\r|   c\n\r|   d\n\r|   e\n\r| ^v move · type filter\n\r"},
		// three of five rows fit with the count of the rest; the cursor row
		// below the frame leaves no row for the hint, which goes first
		{6, "\r? Fruit\n\r| > a\n\r|   b\n\r|   c\n\r| 2 more · ^v move · type filter\n\r"},
		// then the count, then the label
		{4, "\r? Fruit\n\r| > a\n\r|   b\n\r"},
		{3, "\r? Fruit\n\r| > a\n\r"},
	} {
		t.Run(strconv.Itoa(tc.height), func(t *testing.T) {
			d, tt := boundDropdown(t, terst.ASCIITheme(false), 40, tc.height, items)
			assert.Equal(t, tc.want, frame(t, d, tt))
			assert.Equal(t, strings.Count(tc.want, "\n"), d.height())
		})
	}
}

func TestThemedDropdownMoreCountsRowsBelowAfterScrolling(t *testing.T) {
	d, tt := boundDropdown(t, terst.ASCIITheme(false), 40, 6, []any{"a", "b", "c", "d", "e"})
	frame(t, d, tt)
	for range 4 {
		d.pressDown(len(d.displayed))
	}
	// at the bottom nothing is below, and the count row is gone
	assert.Equal(t, "\r? Fruit\n\r|   c\n\r|   d\n\r| > e\n\r| ^v move · type filter\n\r", frame(t, d, tt))
}

type themedFruit struct{ Name, Color string }

func TestThemedDropdownProjectionMatchesAndAnswers(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}, {"cherry", "blue"}}
	// "b" is the start of a detail too, but only the projected name matches
	o, out, _ := themedDropdownOpt(t, terst.ASCIITheme(false), 40, "b", "\r")
	got, err := Dropdown(t.Context(), "Fruit", items, o, WithTemplate(".Name", ".Color"))
	assert.NoError(t, err)
	assert.Equal(t, items[1], got)
	s := out.String()
	// details are on the active row only
	assert.Contains(t, s, "\r| > apple red\n\r|   banana\n\r|   cherry\n")
	assert.Contains(t, s, "\r? Fruit\n\r| > banana yellow\n\r| ^v move · type filter\n")
	assert.True(t, strings.HasSuffix(s, "+ Fruit · banana\n"))
}

func TestThemedDropdownMultiFieldProjects(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}}
	d, tt := boundDropdown(
		t,
		terst.ASCIITheme(false),
		40,
		10,
		[]any{items[0], items[1]},
		WithTemplate(`.Name ", " .Color`),
	)
	assert.Equal(t,
		"\r? Fruit\n\r| > apple, red\n\r|   banana, yellow\n\r| ^v move · type filter\n\r",
		frame(t, d, tt))
}

func TestThemedDropdownHiddenAnswerRetainsNothing(t *testing.T) {
	th := terst.ASCIITheme(false)
	th.Dropdown.Answer = theme.Hidden
	o, out, _ := themedDropdownOpt(t, th, 40, "\r")
	_, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[4A\r"))

	o, out, _ = themedDropdownOpt(t, terst.ASCIITheme(false), 40, "\r")
	_, err = Dropdown(t.Context(), "Fruit", fruits, o, option.WithHide())
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), "\x1b[4A\r"))
}

func TestThemedDropdownSlotPrecedence(t *testing.T) {
	th := terst.ASCIITheme(false)
	th.Choice.Inactive = `{{ printf "-%s" .Value }}`
	th.Dropdown.Active = `[{{ .Value }}]`
	th.Prompt.Hint = theme.Hidden
	th.Dropdown.Label = `{{ .Label }}:`
	d, tt := boundDropdown(t, th, 40, 4, []any{"a", "b", "c", "d"})
	// shared and family edits apply; hidden slots reserve no row
	assert.Equal(t, "\rFruit:\n\r| > [a]\n\r|   -b\n\r", frame(t, d, tt))
}

func TestThemedDropdownKVShowsKeys(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, terst.ASCIITheme(false), 40, "\x1b[B", "\r")
	k, v, err := DropdownKV(t.Context(), "Pick", map[string]int{"b": 2, "a": 1}, o)
	assert.NoError(t, err)
	assert.Equal(t, "b", k)
	assert.Equal(t, 2, v)
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > a\n\r|   b\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · b\n"))
}

func TestThemedDropdownIndexKeepsSourceIndexOfDuplicates(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, terst.ASCIITheme(false), 40, "\x1b[B", "\r")
	i, err := DropdownIndex(t.Context(), "Pick", []any{"same", "same"}, o)
	assert.NoError(t, err)
	assert.Equal(t, 1, i)
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · same\n"))
}

func TestThemedDropdownTypedDefaultSeedsFirstRow(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, terst.ASCIITheme(false), 40, "\r")
	got, err := Dropdown(t.Context(), "Pick", []int{1, 2, 3}, o, option.WithDefault(2))
	assert.NoError(t, err)
	assert.Equal(t, 2, got)
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > 2\n\r|   1\n\r|   3\n"))
}

func TestThemedDropdownOneReturnAnswersWithoutFrame(t *testing.T) {
	o, out, restored := themedDropdownOpt(t, terst.ASCIITheme(false), 40)
	got, err := Dropdown(t.Context(), "Fruit", []string{"apple"}, o, WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, "apple", got)
	assert.True(t, !*restored) // no terminal was acquired
	assert.Equal(t, "+ Fruit · apple\n", out.String())
}

func TestThemedDropdownAutomaticAnswerMatchesProjection(t *testing.T) {
	terst.StrictOutput(t)
	boom := errors.New("boom")
	th := terst.ASCIITheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	// no presentation slot runs without a terminal: only the projection
	th.Prompt.Label = `{{ fails .Label }}`
	th.Choice.Active = `{{ fails .Value }}`
	th.Choice.Value = `{{ printf "z%s" . }}`
	terst.UseTheme(t, th)
	var out bytes.Buffer
	got, err := Dropdown(t.Context(), "Fruit", fruits, option.WithOutput(&out), option.WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, "banana", got)

	got, err = Dropdown(t.Context(), "Fruit", fruits, option.WithOutput(&out),
		WithTemplate(`(printf "y%s" .)`), option.WithDefault("yc"))
	assert.NoError(t, err)
	assert.Equal(t, "cherry", got)

	assert.Equal(t, "", out.String())
}

func TestThemedDropdownSearchSkipsDecoration(t *testing.T) {
	th := terst.ASCIITheme(false)
	th.Choice.Active = `{{ .Value }} zzz`
	th.Choice.Inactive = `{{ .Value }} zzz`
	d, tt := boundDropdown(t, th, 40, 10, []any{"apple", "banana"})
	frame(t, d, tt)
	// the presentation of the rows is not searchable, only their content
	assert.Equal(t, 0, len(d.trie.Prefix("zzz")))
	assert.Equal(t, []int{1}, d.trie.Prefix("b"))
	assert.Equal(t, []string{"apple", "banana"}, d.search)

	// a late item is projected and matched like the others
	assert.NoError(t, d.addItem(tt.Height, "blueberry"))
	assert.Equal(t, []int{1, 2}, d.trie.Prefix("b"))
	assert.Equal(t, "blueberry", d.content[2].String())
}

func TestThemedDropdownLazyOneReturnAndProducerError(t *testing.T) {
	o, out, restored := themedDropdownOpt(t, terst.ASCIITheme(false), 40)
	got, err := DropdownLazy(t.Context(), "Fruit", lazySeq("apple"), o, WithOneReturn())
	assert.NoError(t, err)
	assert.Equal(t, "apple", got)
	assert.True(t, *restored)
	assert.True(t, strings.HasSuffix(out.String(), "+ Fruit · apple\n"))

	boom := errors.New("boom")
	failing := func(yield func(string, error) bool) {
		if yield("apple", nil) {
			yield("", boom)
		}
	}
	o, out, restored = themedDropdownOpt(t, terst.ASCIITheme(false), 40)
	_, err = DropdownLazy(t.Context(), "Fruit", failing, o)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedDropdownHelperErrorRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := terst.ASCIITheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Choice.Inactive = `{{ fails .Value }}`
	o, _, restored := themedDropdownOpt(t, th, 40, "\r")
	_, err := Dropdown(t.Context(), "Fruit", fruits, o)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Choice.Inactive")
	assert.True(t, *restored)
}

func TestThemedDropdownRolesStyleFocusAndRows(t *testing.T) {
	terst.WithEnv(t, "FORCE_COLOR=3")
	th := terst.ASCIITheme(false)
	th.Light = true
	d, tt := boundDropdown(t, th, 40, 10, []any{"a", "b"})
	got := frame(t, d, tt)
	// the focus mark and the active value take the accent, inactive values the foreground
	roles := d.look.Roles
	assert.True(t, len(roles.Accent) > 0 && len(roles.Foreground) > 0)
	assert.Contains(t, got, string(roles.Accent)+">")
	assert.Contains(t, got, string(roles.Accent)+"a")
	assert.Contains(t, got, string(roles.Foreground)+"b")
}

func TestThemedDropdownFullHeightFrameKeepsLabelOnScreen(t *testing.T) {
	o, out, _ := themedDropdownOpt(t, terst.ASCIITheme(false), 60, "\x1b[B", "\x1b[B", "\x1b[A", "\x1b[B", "\r")
	six := option.OpT(func(d *dropdown) error {
		next := d.makeTermIO
		d.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			tt, err := next(in, out)
			tt.Height = 6
			return tt, err
		}
		return nil
	})
	got, err := Dropdown(t.Context(), "Fruit", []string{"a", "b", "c", "d", "e"}, o, six)
	assert.NoError(t, err)
	assert.Equal(t, "c", got)
	frameStaysOnScreen(t, out.String())
}
