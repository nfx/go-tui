// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/tcommon"
	"github.com/nfx/go-tui/internal/term"
	"github.com/nfx/go-tui/theme"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/terst"
)

func TestMultichoiceRenderInitializes(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"one", "two"}
	io := tcommon.FixedTermIO(20, 6).IO()

	frame := &bytes.Buffer{}
	assert.NoError(t, m.prepare(nil))
	assert.NoError(t, m.bindRun(t.Context(), io))

	assert.NoError(t, m.render(io, frame))
}

func TestMultichoiceRenderMoreItems(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"short", "veryverylong", "mid", "tail"}
	m.active = 0
	io := tcommon.FixedTermIO(5, 4).IO()
	frame := &bytes.Buffer{}
	assert.NoError(t, m.prepare(nil))
	assert.NoError(t, m.bindRun(t.Context(), io))

	assert.NoError(t, m.render(io, frame))
}

type choiceTemplateFailure struct{ err error }

// Label exposes a sentinel error through template execution.
func (x choiceTemplateFailure) Label() (string, error) { return "", x.err }

func TestMultichoiceTemplateErrorPreservesUnderlyingCause(t *testing.T) {
	cause := errors.New("label failed")
	terst.UseKeys(t)
	eager, err := Multichoice(
		t.Context(),
		"Eager",
		[]choiceTemplateFailure{{cause}},
		WithTemplate(".Label"),
	)
	if eager != nil || !errors.Is(err, cause) {
		t.Fatal(eager, err)
	}
	seq := func(yield func(choiceTemplateFailure, error) bool) { yield(choiceTemplateFailure{cause}, nil) }
	lazy, err := MultichoiceLazy(
		t.Context(),
		"Lazy",
		seq,
		WithTemplate(".Label"),
	)
	if lazy != nil || !errors.Is(err, cause) {
		t.Fatal(lazy, err)
	}
}

// themedMultichoiceOpt routes a themed multichoice through a 60-column scripted terminal.
func themedMultichoiceOpt(
	t *testing.T,
	th theme.Theme,
	keys ...string,
) (o option.Opt, out *bytes.Buffer, restored *bool) {
	t.Helper()
	s := scripted(t, th, 60, keys...)
	return option.OpT(func(m *multichoice) error {
		m.in, m.out, m.makeTermIO = s.in, s.out, s.makeTermIO
		return nil
	}), s.out, s.restored
}

// boundMultichoice prepares a themed prompt for frame tests at the given terminal height.
func boundMultichoice(t *testing.T, th theme.Theme, height int, items []any, o ...option.Opt) (*multichoice, *term.IO) {
	t.Helper()
	terst.UseTheme(t, th)
	m := newMultichoice(t.Context())
	m.Label, m.Items = "Fruit", items
	assert.NoError(t, m.prepare(o))
	tt := &term.IO{Out: &bytes.Buffer{}, Width: 60, Height: height}
	assert.NoError(t, m.bindRun(t.Context(), tt))
	return m, tt
}

// mframe draws one frame of m on tt.
func mframe(t *testing.T, m *multichoice, tt *term.IO) string {
	t.Helper()
	var buf bytes.Buffer
	assert.NoError(t, m.render(tt, &buf))
	return buf.String()
}

const multichoiceHelp = "| ^v move · space toggle · type filter\n\r"

func TestThemedMultichoiceGuidedFrameStatesAndAnswer(t *testing.T) {
	o, out, restored := themedMultichoiceOpt(t, terst.ASCIITheme(), " ", "\x1b[B", " ", "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"apple", "banana"}, got)
	assert.True(t, *restored)
	s := out.String()
	// the label has its own row; the rows and the hint are behind the guide
	assert.True(t, strings.HasPrefix(s, ""+
		"\r? Fruit\n\r| > o apple\n\r|   o banana\n\r|   o cherry\n\r"+multichoiceHelp))
	// focused and checked, each marked exactly once
	assert.Contains(t, s, "\r? Fruit\n\r| > x apple\n\r|   o banana\n")
	assert.Contains(t, s, "\r? Fruit\n\r|   x apple\n\r| > o banana\n\r|   o cherry\n")
	assert.Contains(t, s, "\r? Fruit\n\r|   x apple\n\r| > x banana\n\r|   o cherry\n")
	// confirming erases the five rows once and retains the collection
	erase := "\x1b[5A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[4A\r"
	assert.True(t, strings.HasSuffix(s, erase+"+ Fruit · apple, banana\n"))
}

func TestThemedMultichoiceEmptySelectionAnswersNone(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, terst.ASCIITheme(), "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{}, got)
	assert.True(t, strings.HasSuffix(out.String(), "\r+ Fruit · none\n"))
}

func TestThemedMultichoiceInlineHasNoGuide(t *testing.T) {
	inline := terst.ASCIITheme()
	inline.Inline = true
	m, tt := boundMultichoice(t, inline, 10, []any{"apple", "banana"})
	m.handleKey(' ')
	assert.Equal(t,
		"\r? Fruit\n\r> x apple\n\r  o banana\n\r^v move · space toggle · type filter\n\r",
		mframe(t, m, tt))
}

func TestThemedMultichoiceCheckedHiddenItemsStayInSourceOrder(t *testing.T) {
	// check cherry, then apple after filtering cherry out of view
	o, out, _ := themedMultichoiceOpt(t, terst.ASCIITheme(),
		"\x1b[B", "\x1b[B", " ", "a", " ", "\x7f", "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"apple", "cherry"}, got)
	s := out.String()
	assert.Contains(t, s, "\r? Fruit\n\r| > x apple\n\r"+multichoiceHelp)
	assert.True(t, strings.HasSuffix(s, "+ Fruit · apple, cherry\n"))
}

func TestThemedMultichoiceEmptyState(t *testing.T) {
	m, tt := boundMultichoice(t, terst.ASCIITheme(), 10, []any{"apple", "banana"})
	m.handleKey(' ')
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r| No matches\n\r"+multichoiceHelp, mframe(t, m, tt))

	th := terst.ASCIITheme()
	th.Choice.Empty = `{{ printf "nothing for %q" .Query }}`
	m, tt = boundMultichoice(t, th, 10, []any{"apple"})
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r| nothing for \"zz\"\n\r"+multichoiceHelp, mframe(t, m, tt))

	// a hidden empty state reserves no row; the checked item still answers
	th.Multichoice.Empty = theme.Hidden
	m, tt = boundMultichoice(t, th, 10, []any{"apple"})
	m.handleKey(' ')
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r"+multichoiceHelp, mframe(t, m, tt))
	assert.Equal(t, []int{0}, m.indices())
}

func TestThemedMultichoiceLoadingShowsNoEmptyState(t *testing.T) {
	terst.UseASCII(t)
	m := newMultichoice(t.Context())
	m.loading, m.Label = true, "Fruit"
	assert.NoError(t, m.prepare(nil))
	tt := &term.IO{Out: &bytes.Buffer{}, Width: 60, Height: 10}
	assert.NoError(t, m.bindRun(t.Context(), tt))
	// nothing has arrived yet, which is not an empty match
	assert.Equal(t, "\r? Fruit\n\r"+multichoiceHelp, mframe(t, m, tt))
	assert.NoError(t, m.addItem("apple"))
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r| No matches\n\r"+multichoiceHelp, mframe(t, m, tt))
}

func TestThemedMultichoiceMoreRowAndShortTerminals(t *testing.T) {
	items := []any{"a", "b", "c", "d", "e"}
	for _, tc := range []struct {
		height int
		want   string
	}{
		{10, "\r? Fruit\n\r| > o a\n\r|   o b\n\r|   o c\n\r|   o d\n\r|   o e\n\r" + multichoiceHelp},
		// three of five rows fit with the count of the rest; the cursor row
		// below the frame leaves no row for the hint, which goes first
		{6, "\r? Fruit\n\r| > o a\n\r|   o b\n\r|   o c\n\r| 2 more · ^v move · space toggle · type filter\n\r"},
		// then the count, then the label
		{4, "\r? Fruit\n\r| > o a\n\r|   o b\n\r"},
		{3, "\r? Fruit\n\r| > o a\n\r"},
	} {
		t.Run(strconv.Itoa(tc.height), func(t *testing.T) {
			m, tt := boundMultichoice(t, terst.ASCIITheme(), tc.height, items)
			assert.Equal(t, tc.want, mframe(t, m, tt))
		})
	}
	m, tt := boundMultichoice(t, terst.ASCIITheme(), 6, items)
	for range 4 {
		m.handleKey('↓')
	}
	// at the bottom nothing is below, and the count row is gone
	assert.Equal(t, "\r? Fruit\n\r|   o c\n\r|   o d\n\r| > o e\n\r"+multichoiceHelp, mframe(t, m, tt))
}

func TestThemedMultichoiceProjectionMatchesAndAnswers(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}, {"cherry", "blue"}}
	// "b" is the start of a detail too, but only the projected name matches
	o, out, _ := themedMultichoiceOpt(t, terst.ASCIITheme(), "b", " ", "\r")
	got, err := Multichoice(t.Context(), "Fruit", items, o, WithTemplate(".Name", ".Color"))
	assert.NoError(t, err)
	assert.Equal(t, []themedFruit{items[1]}, got)
	s := out.String()
	// details are on the active row only
	assert.Contains(t, s, "\r| > o apple red\n\r|   o banana\n\r|   o cherry\n")
	assert.Contains(t, s, "\r? Fruit\n\r| > x banana yellow\n\r"+multichoiceHelp)
	assert.True(t, strings.HasSuffix(s, "+ Fruit · banana\n"))
}

func TestThemedMultichoiceMultiFieldProjects(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}}
	m, tt := boundMultichoice(
		t,
		terst.ASCIITheme(),
		10,
		[]any{items[0], items[1]},
		WithTemplate(`.Name ", " .Color`),
	)
	assert.Equal(t,
		"\r? Fruit\n\r| > o apple, red\n\r|   o banana, yellow\n\r"+multichoiceHelp,
		mframe(t, m, tt))
}

func TestThemedMultichoiceHiddenAnswerRetainsNothing(t *testing.T) {
	erase := "\x1b[4A\r"
	th := terst.ASCIITheme()
	th.Multichoice.Answer = theme.Hidden
	o, out, _ := themedMultichoiceOpt(t, th, " ", "\r")
	_, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), erase))

	o, out, _ = themedMultichoiceOpt(t, terst.ASCIITheme(), " ", "\r")
	_, err = Multichoice(t.Context(), "Fruit", fruits, o, option.WithHideAnswer())
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), erase))
}

func TestThemedMultichoiceSlotPrecedence(t *testing.T) {
	th := terst.ASCIITheme()
	th.Choice.Inactive = `{{ printf "-%s" .Value }}`
	th.Multichoice.Active = `[{{ .Value }}]`
	th.Dropdown.Inactive = `dropdown only`
	th.Multichoice.Row = `{{ if .Selected }}+{{ else }}.{{ end }}{{ .Value }}#{{ .Index }}`
	th.Prompt.Hint = theme.Hidden
	th.Multichoice.Label = `{{ .Label }}:`
	m, tt := boundMultichoice(t, th, 4, []any{"a", "b", "c", "d"})
	m.handleKey('↓')
	m.handleKey(' ')
	// shared and family edits apply, other families do not; hidden slots reserve no row
	assert.Equal(t, "\rFruit:\n\r| .-a#0\n\r| +[b]#1\n\r", mframe(t, m, tt))
}

func TestThemedMultichoiceStateSlotsSeeSelection(t *testing.T) {
	th := terst.ASCIITheme()
	th.Choice.Inactive = `{{ if .Selected }}{{ upper .Value }}{{ else }}{{ .Value }}{{ end }}`
	th.Funcs = template.FuncMap{"upper": strings.ToUpper}
	m, tt := boundMultichoice(t, th, 10, []any{"apple", "banana"})
	m.handleKey(' ')
	m.handleKey('↓')
	assert.Contains(t, mframe(t, m, tt), "|   x APPLE\n\r| > o banana\n")
}

func TestThemedMultichoiceAnswerValueAndSharedAnswer(t *testing.T) {
	th := terst.ASCIITheme()
	th.Prompt.Answer = `{{ .Label }}: {{ .Value }} ({{ .Count }})`
	th.Multichoice.AnswerValue = `{{ range .Values }}[{{ . }}]{{ end }}`
	o, out, _ := themedMultichoiceOpt(t, th, "\x1b[B", " ", "\x1b[B", " ", "\r")
	_, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), "\rFruit: [banana][cherry] (2)\n"))
}

func TestThemedMultichoiceKVShowsKeys(t *testing.T) {
	items := map[string]int{"b": 2, "a": 1}
	o, out, _ := themedMultichoiceOpt(t, terst.ASCIITheme(), "\x1b[B", " ", "\r")
	keys, err := MultichoiceKVK(t.Context(), "Pick", items, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"b"}, keys)
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > o a\n\r|   o b\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · b\n"))

	o, out, _ = themedMultichoiceOpt(t, terst.ASCIITheme(), " ", "\r")
	values, err := MultichoiceKVV(t.Context(), "Pick", items, o)
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, values)
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · a\n"))
}

func TestThemedMultichoiceTypedDefaultChecksAfterBinding(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, terst.ASCIITheme(), "\r")
	got, err := Multichoice(t.Context(), "Pick", []string{"a", "b", "c"}, o, option.WithDefault([]string{"c", "a"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, got)
	// the first checked entry takes the focus
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > x a\n\r|   o b\n\r|   x c\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · a, c\n"))

	o, _, restored := themedMultichoiceOpt(t, terst.ASCIITheme(), "\r")
	_, err = Multichoice(t.Context(), "Pick", []string{"a"}, o, option.WithDefault([]string{"zz"}))
	assert.ErrorIs(t, err, errs.ErrInvalidOption)
	assert.True(t, *restored)
}

func TestThemedMultichoiceHintMatchesProjection(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, terst.ASCIITheme(), "\r")
	got, err := Multichoice(t.Context(), "Pick", fruits, o, WithTemplate(`(printf "z%s" .)`), option.WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · zbanana\n"))
}

func TestThemedMultichoiceAutomaticAnswerRunsOnlyProjection(t *testing.T) {
	terst.Quiet(t)
	boom := errors.New("boom")
	th := terst.ASCIITheme()
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	// no presentation slot runs without a terminal: only the projection
	th.Prompt.Label = `{{ fails .Label }}`
	th.Choice.Active = `{{ fails .Value }}`
	th.Choice.Inactive = `{{ fails .Value }}`
	th.Multichoice.Row = `{{ fails .Value }}`
	th.Multichoice.AnswerValue = `{{ range .Values }}{{ fails . }}{{ end }}`
	th.Choice.Value = `{{ printf "z%s" . }}`
	terst.UseTheme(t, th)
	got, err := Multichoice(t.Context(), "Fruit", fruits, option.WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)

	got, err = Multichoice(t.Context(), "Fruit", fruits, WithTemplate(`(printf "y%s" .)`), option.WithDefault("yc"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"cherry"}, got)

	got, err = MultichoiceLazy(
		t.Context(),
		"Fruit",
		lazySeq("apple", "banana"),
		option.WithDefault("zb"),
	)
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)
}

func TestThemedMultichoiceSearchSkipsDecoration(t *testing.T) {
	th := terst.ASCIITheme()
	th.Choice.Active = `{{ .Value }} zzz`
	th.Choice.Inactive = `{{ .Value }} zzz`
	th.Multichoice.Row = `zzz {{ .Value }}`
	m, tt := boundMultichoice(t, th, 10, []any{"apple", "banana"})
	mframe(t, m, tt)
	// the presentation of the rows is not searchable, only their content
	assert.Equal(t, 0, len(m.trie.Prefix("zzz")))
	assert.Equal(t, []int{1}, m.trie.Prefix("b"))

	// a late item is projected and matched like the others
	assert.NoError(t, m.addItem("blueberry"))
	assert.Equal(t, []int{1, 2}, m.trie.Prefix("b"))
	assert.Equal(t, "blueberry", m.content[2].String())
}

func TestThemedMultichoiceProjectsOnceAndDrawsLazily(t *testing.T) {
	calls := map[string]int{}
	th := terst.ASCIITheme()
	th.Funcs = template.FuncMap{"count": func(kind, s string) string { calls[kind+":"+s]++; return s }}
	th.Choice.Value = `{{ count "value" . }}`
	th.Choice.Details = `{{ count "details" . }}`
	m, tt := boundMultichoice(t, th, 4, []any{"a", "b", "c", "d"})
	for range 3 {
		mframe(t, m, tt)
	}
	m.handleKey(' ')
	var out bytes.Buffer
	m.out = &out
	assert.NoError(t, m.themedAnswer())
	assert.Equal(t, "+ Fruit · a\n", out.String())
	// every item projects once, and only drawn items render their details
	assert.Equal(t, map[string]int{
		"value:a": 1, "value:b": 1, "value:c": 1, "value:d": 1,
		"details:a": 1, "details:b": 1,
	}, calls)
}

func TestThemedMultichoiceLazyConfirmsAfterExhaustion(t *testing.T) {
	terst.UseASCII(t)
	got, err := answerWhenShown(t, "x banana", terst.KeyEnter, func() ([]string, error) {
		return MultichoiceLazy(t.Context(), "Fruit", lazySeq("apple", "banana"), option.WithDefault("banana"))
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)
}

func TestThemedMultichoiceLazyProducerErrorRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	failing := func(yield func(string, error) bool) {
		if yield("apple", nil) {
			yield("", boom)
		}
	}
	o, out, restored := themedMultichoiceOpt(t, terst.ASCIITheme())
	_, err := MultichoiceLazy(t.Context(), "Fruit", failing, o)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedMultichoiceHelperErrorRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := terst.ASCIITheme()
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Multichoice.Row = `{{ fails .Value }}`
	o, _, restored := themedMultichoiceOpt(t, th, "\r")
	_, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Multichoice.Row")
	assert.True(t, *restored)

	th = terst.ASCIITheme()
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Multichoice.AnswerValue = `{{ fails "x" }}`
	o, out, restored := themedMultichoiceOpt(t, th, "\r")
	_, err = Multichoice(t.Context(), "Fruit", fruits, o)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedMultichoicePasteFiltersAndCancellationRestores(t *testing.T) {
	o, _, _ := themedMultichoiceOpt(t, terst.ASCIITheme(), "\x1b[200~ban\x1b[201~", " ", "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)

	ctx, cancel := context.WithCancel(t.Context())
	o, out, restored := themedMultichoiceOpt(t, terst.ASCIITheme())
	acquired := make(chan struct{})
	signal := option.OpT(func(m *multichoice) error {
		next := m.makeTermIO
		m.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			defer close(acquired)
			return next(in, out)
		}
		return nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := Multichoice(ctx, "Fruit", fruits, o, signal)
		done <- err
	}()
	<-acquired
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("prompt stayed blocked after cancellation")
	}
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedMultichoiceRolesStyleMarks(t *testing.T) {
	terst.WithEnv(t, "FORCE_COLOR=3")
	th := terst.ASCIITheme()
	th.Light = true
	m, tt := boundMultichoice(t, th, 10, []any{"a", "b"})
	m.handleKey(' ')
	got := mframe(t, m, tt)
	// focus and checked marks take the accent, the unchecked mark is muted
	roles := m.look.Roles
	assert.True(t, len(roles.Accent) > 0 && len(roles.Muted) > 0)
	assert.Contains(t, got, string(roles.Accent)+">")
	assert.Contains(t, got, string(roles.Accent)+"x")
	assert.Contains(t, got, string(roles.Muted)+"o")
}

// frameStaysOnScreen replays out, the output of a prompt over a, b, c, d, e
// on a terminal of six rows that confirmed c, on a bounded screen: the first
// frame shows the label at the top, no redraw scrolls a row away, and cleanup
// leaves only the answer.
func frameStaysOnScreen(t *testing.T, out string) {
	t.Helper()
	first := out[:strings.Index(out, "\x1b[")]
	assert.Equal(t, "? Fruit", strings.Split(visibleOn(t, 6, first), "\n")[0])
	// a row scrolled away from six rows would still show on a screen without
	// a bottom, so after every redraw both screens show the same
	for i := range len(out) {
		if strings.HasPrefix(out[i:], "\x1b[") || i == len(out)-1 {
			assert.Equal(t, visibleOn(t, 0, out[:i+1]), visibleOn(t, 6, out[:i+1]))
		}
	}
	assert.Equal(t, "+ Fruit · c", visibleOn(t, 6, out))
}

// visibleOn is what out leaves on a screen 60 columns wide and height rows high.
func visibleOn(t *testing.T, height int, out string) string {
	t.Helper()
	screen := terst.NewOutput(t)
	screen.Resize(60, height)
	_, err := screen.Write([]byte(out))
	assert.NoError(t, err)
	return screen.Visible()
}

func TestThemedMultichoiceFullHeightFrameKeepsLabelOnScreen(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, terst.ASCIITheme(), "\x1b[B", "\x1b[B", " ", "\x1b[A", "\x1b[B", "\r")
	six := option.OpT(func(m *multichoice) error {
		next := m.makeTermIO
		m.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			tt, err := next(in, out)
			tt.Height = 6
			return tt, err
		}
		return nil
	})
	got, err := Multichoice(t.Context(), "Fruit", []string{"a", "b", "c", "d", "e"}, o, six)
	assert.NoError(t, err)
	assert.Equal(t, []string{"c"}, got)
	frameStaysOnScreen(t, out.String())
}
