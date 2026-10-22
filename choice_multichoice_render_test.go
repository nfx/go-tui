// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

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

	"github.com/nfx/go-tui/internal/assert"
)

func TestMultichoiceRenderInitializes(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"one", "two"}
	io := newTestTermIO(20, 6)

	frame := &bytes.Buffer{}
	assert.NoError(t, m.prepare(nil))
	assert.NoError(t, m.bindRun(t.Context(), io))

	assert.NoError(t, m.render(io, frame))
}

func TestMultichoiceRenderMoreItems(t *testing.T) {
	m := newMultichoice(t.Context())
	m.Items = []any{"short", "veryverylong", "mid", "tail"}
	m.active = 0
	io := newTestTermIO(5, 4)
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
	eager, err := Multichoice(
		t.Context(),
		"Eager",
		[]choiceTemplateFailure{{cause}},
		WithTemplate(".Label"),
		WithOutput(io.Discard),
	)
	if eager != nil || !errors.Is(err, cause) {
		t.Fatal(eager, err)
	}
	input, typing := io.Pipe()
	defer func() { assert.NoError(t, input.Close()) }()
	defer func() { assert.NoError(t, typing.Close()) }()
	seq := func(yield func(choiceTemplateFailure, error) bool) { yield(choiceTemplateFailure{cause}, nil) }
	lazy, err := MultichoiceLazy(
		t.Context(),
		"Lazy",
		seq,
		WithTemplate(".Label"),
		WithInput(input),
		WithOutput(io.Discard),
	)
	if lazy != nil || !errors.Is(err, cause) {
		t.Fatal(lazy, err)
	}
}

// themedMultichoiceOpt routes a themed multichoice through a 60-column scripted terminal.
func themedMultichoiceOpt(t *testing.T, th Theme, keys ...string) (o opt, out *bytes.Buffer, restored *bool) {
	t.Helper()
	s := scripted(t, th, 60, keys...)
	return opT(func(m *multichoice) error {
		m.in, m.out, m.makeTermIO = s.in, s.out, s.makeTermIO
		return nil
	}), s.out, s.restored
}

// boundMultichoice prepares a themed prompt for frame tests at the given terminal height.
func boundMultichoice(t *testing.T, th Theme, height int, items []any, o ...opt) (*multichoice, *termIO) {
	t.Helper()
	useTheme(t, th)
	m := newMultichoice(t.Context())
	m.Label, m.Items = "Fruit", items
	assert.NoError(t, m.prepare(o))
	tt := &termIO{out: &bytes.Buffer{}, Width: 60, Height: height}
	assert.NoError(t, m.bindRun(t.Context(), tt))
	return m, tt
}

// mframe draws one frame of m on tt.
func mframe(t *testing.T, m *multichoice, tt *termIO) string {
	t.Helper()
	var buf bytes.Buffer
	assert.NoError(t, m.render(tt, &buf))
	return buf.String()
}

const multichoiceHelp = "| ^v move · space toggle · type filter\n\r"

func TestThemedMultichoiceGuidedFrameStatesAndAnswer(t *testing.T) {
	o, out, restored := themedMultichoiceOpt(t, asciiTheme(false), " ", "\x1b[B", " ", "\r")
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
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{}, got)
	assert.True(t, strings.HasSuffix(out.String(), "\r+ Fruit · none\n"))
}

func TestThemedMultichoiceInlineHasNoGuide(t *testing.T) {
	m, tt := boundMultichoice(t, asciiTheme(true), 10, []any{"apple", "banana"})
	m.handleKey(' ')
	assert.Equal(t,
		"\r? Fruit\n\r> x apple\n\r  o banana\n\r^v move · space toggle · type filter\n\r",
		mframe(t, m, tt))
}

func TestThemedMultichoiceCheckedHiddenItemsStayInSourceOrder(t *testing.T) {
	// check cherry, then apple after filtering cherry out of view
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false),
		"\x1b[B", "\x1b[B", " ", "a", " ", "\x7f", "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"apple", "cherry"}, got)
	s := out.String()
	assert.Contains(t, s, "\r? Fruit\n\r| > x apple\n\r"+multichoiceHelp)
	assert.True(t, strings.HasSuffix(s, "+ Fruit · apple, cherry\n"))
}

func TestThemedMultichoiceEmptyState(t *testing.T) {
	m, tt := boundMultichoice(t, asciiTheme(false), 10, []any{"apple", "banana"})
	m.handleKey(' ')
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r| No matches\n\r"+multichoiceHelp, mframe(t, m, tt))

	th := asciiTheme(false)
	th.Templates.Choice.Empty = `{{ printf "nothing for %q" .Query }}`
	m, tt = boundMultichoice(t, th, 10, []any{"apple"})
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r| nothing for \"zz\"\n\r"+multichoiceHelp, mframe(t, m, tt))

	// a hidden empty state reserves no row; the checked item still answers
	th.Templates.Multichoice.Empty = Hidden
	m, tt = boundMultichoice(t, th, 10, []any{"apple"})
	m.handleKey(' ')
	m.filter("zz")
	assert.Equal(t, "\r? Fruit\n\r"+multichoiceHelp, mframe(t, m, tt))
	assert.Equal(t, []int{0}, m.indices())
}

func TestThemedMultichoiceLoadingShowsNoEmptyState(t *testing.T) {
	useTheme(t, asciiTheme(false))
	m := newMultichoice(t.Context())
	m.loading, m.Label = true, "Fruit"
	assert.NoError(t, m.prepare(nil))
	tt := &termIO{out: &bytes.Buffer{}, Width: 60, Height: 10}
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
			m, tt := boundMultichoice(t, asciiTheme(false), tc.height, items)
			assert.Equal(t, tc.want, mframe(t, m, tt))
		})
	}
	m, tt := boundMultichoice(t, asciiTheme(false), 6, items)
	for range 4 {
		m.handleKey('↓')
	}
	// at the bottom nothing is below, and the count row is gone
	assert.Equal(t, "\r? Fruit\n\r|   o c\n\r|   o d\n\r| > o e\n\r"+multichoiceHelp, mframe(t, m, tt))
}

func TestThemedMultichoiceProjectionMatchesAndAnswers(t *testing.T) {
	items := []themedFruit{{"apple", "red"}, {"banana", "yellow"}, {"cherry", "blue"}}
	// "b" is the start of a detail too, but only the projected name matches
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "b", " ", "\r")
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
	m, tt := boundMultichoice(t, asciiTheme(false), 10, []any{items[0], items[1]}, WithTemplate(`.Name ", " .Color`))
	assert.Equal(t,
		"\r? Fruit\n\r| > o apple, red\n\r|   o banana, yellow\n\r"+multichoiceHelp,
		mframe(t, m, tt))
}

func TestThemedMultichoiceHiddenAnswerRetainsNothing(t *testing.T) {
	erase := "\x1b[4A\r"
	th := asciiTheme(false)
	th.Templates.Multichoice.Answer = Hidden
	o, out, _ := themedMultichoiceOpt(t, th, " ", "\r")
	_, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), erase))

	o, out, _ = themedMultichoiceOpt(t, asciiTheme(false), " ", "\r")
	_, err = Multichoice(t.Context(), "Fruit", fruits, o, WithHide())
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), erase))
}

func TestThemedMultichoiceSlotPrecedence(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Choice.Inactive = `{{ printf "-%s" .Value }}`
	th.Templates.Multichoice.Active = `[{{ .Value }}]`
	th.Templates.Dropdown.Inactive = `dropdown only`
	th.Templates.Multichoice.Row = `{{ if .Selected }}+{{ else }}.{{ end }}{{ .Value }}#{{ .Index }}`
	th.Templates.Prompt.Hint = Hidden
	th.Templates.Multichoice.Label = `{{ .Label }}:`
	m, tt := boundMultichoice(t, th, 4, []any{"a", "b", "c", "d"})
	m.handleKey('↓')
	m.handleKey(' ')
	// shared and family edits apply, other families do not; hidden slots reserve no row
	assert.Equal(t, "\rFruit:\n\r| .-a#0\n\r| +[b]#1\n\r", mframe(t, m, tt))
}

func TestThemedMultichoiceStateSlotsSeeSelection(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Choice.Inactive = `{{ if .Selected }}{{ upper .Value }}{{ else }}{{ .Value }}{{ end }}`
	th.Funcs = template.FuncMap{"upper": strings.ToUpper}
	m, tt := boundMultichoice(t, th, 10, []any{"apple", "banana"})
	m.handleKey(' ')
	m.handleKey('↓')
	assert.Contains(t, mframe(t, m, tt), "|   x APPLE\n\r| > o banana\n")
}

func TestThemedMultichoiceAnswerValueAndSharedAnswer(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Prompt.Answer = `{{ .Label }}: {{ .Value }} ({{ .Count }})`
	th.Templates.Multichoice.AnswerValue = `{{ range .Values }}[{{ . }}]{{ end }}`
	o, out, _ := themedMultichoiceOpt(t, th, "\x1b[B", " ", "\x1b[B", " ", "\r")
	_, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), "\rFruit: [banana][cherry] (2)\n"))
}

func TestThemedMultichoiceKVShowsKeys(t *testing.T) {
	items := map[string]int{"b": 2, "a": 1}
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\x1b[B", " ", "\r")
	keys, err := MultichoiceKVK(t.Context(), "Pick", items, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"b"}, keys)
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > o a\n\r|   o b\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · b\n"))

	o, out, _ = themedMultichoiceOpt(t, asciiTheme(false), " ", "\r")
	values, err := MultichoiceKVV(t.Context(), "Pick", items, o)
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, values)
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · a\n"))
}

func TestThemedMultichoiceTypedDefaultChecksAfterBinding(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\r")
	got, err := Multichoice(t.Context(), "Pick", []string{"a", "b", "c"}, o, WithDefault([]string{"c", "a"}))
	assert.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, got)
	// the first checked entry takes the focus
	assert.True(t, strings.HasPrefix(out.String(), "\r? Pick\n\r| > x a\n\r|   o b\n\r|   x c\n"))
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · a, c\n"))

	o, _, restored := themedMultichoiceOpt(t, asciiTheme(false), "\r")
	_, err = Multichoice(t.Context(), "Pick", []string{"a"}, o, WithDefault([]string{"zz"}))
	assert.ErrorIs(t, err, ErrInvalidOption)
	assert.True(t, *restored)
}

func TestThemedMultichoiceHintMatchesProjection(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\r")
	got, err := Multichoice(t.Context(), "Pick", fruits, o, WithTemplate(`(printf "z%s" .)`), WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)
	assert.True(t, strings.HasSuffix(out.String(), "+ Pick · zbanana\n"))
}

func TestThemedMultichoiceAutomaticAnswerRunsOnlyProjection(t *testing.T) {
	strictOutput(t)
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	// no presentation slot runs without a terminal: only the projection
	th.Templates.Prompt.Label = `{{ fails .Label }}`
	th.Templates.Choice.Active = `{{ fails .Value }}`
	th.Templates.Choice.Inactive = `{{ fails .Value }}`
	th.Templates.Multichoice.Row = `{{ fails .Value }}`
	th.Templates.Multichoice.AnswerValue = `{{ range .Values }}{{ fails . }}{{ end }}`
	th.Templates.Choice.Value = `{{ printf "z%s" . }}`
	useTheme(t, th)
	var out bytes.Buffer
	got, err := Multichoice(t.Context(), "Fruit", fruits, WithOutput(&out), WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)

	got, err = Multichoice(t.Context(), "Fruit", fruits, WithOutput(&out),
		WithTemplate(`(printf "y%s" .)`), WithDefault("yc"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"cherry"}, got)

	got, err = MultichoiceLazy(t.Context(), "Fruit", lazySeq("apple", "banana"), WithOutput(&out), WithDefault("zb"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)
	assert.Equal(t, "", out.String())
}

func TestThemedMultichoiceSearchSkipsDecoration(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Choice.Active = `{{ .Value }} zzz`
	th.Templates.Choice.Inactive = `{{ .Value }} zzz`
	th.Templates.Multichoice.Row = `zzz {{ .Value }}`
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
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"count": func(kind, s string) string { calls[kind+":"+s]++; return s }}
	th.Templates.Choice.Value = `{{ count "value" . }}`
	th.Templates.Choice.Details = `{{ count "details" . }}`
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
	useTheme(t, asciiTheme(false))
	stream := &completionChecklistIO{ready: make(chan struct{}), marker: "x banana"}
	got, err := MultichoiceLazy(t.Context(), "Fruit", lazySeq("apple", "banana"),
		WithDefault("banana"), WithInput(stream), WithOutput(stream))
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
	o, out, restored := themedMultichoiceOpt(t, asciiTheme(false))
	_, err := MultichoiceLazy(t.Context(), "Fruit", failing, o)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedMultichoiceHelperErrorRestoresTerminal(t *testing.T) {
	boom := errors.New("boom")
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Templates.Multichoice.Row = `{{ fails .Value }}`
	o, _, restored := themedMultichoiceOpt(t, th, "\r")
	_, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Templates.Multichoice.Row")
	assert.True(t, *restored)

	th = asciiTheme(false)
	th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
	th.Templates.Multichoice.AnswerValue = `{{ fails "x" }}`
	o, out, restored := themedMultichoiceOpt(t, th, "\r")
	_, err = Multichoice(t.Context(), "Fruit", fruits, o)
	assert.ErrorIs(t, err, boom)
	assert.True(t, *restored)
	assert.True(t, !strings.Contains(out.String(), "+ Fruit"))
}

func TestThemedMultichoicePasteFiltersAndCancellationRestores(t *testing.T) {
	o, _, _ := themedMultichoiceOpt(t, asciiTheme(false), "\x1b[200~ban\x1b[201~", " ", "\r")
	got, err := Multichoice(t.Context(), "Fruit", fruits, o)
	assert.NoError(t, err)
	assert.Equal(t, []string{"banana"}, got)

	ctx, cancel := context.WithCancel(t.Context())
	o, out, restored := themedMultichoiceOpt(t, asciiTheme(false))
	acquired := make(chan struct{})
	signal := opT(func(m *multichoice) error {
		next := m.makeTermIO
		m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
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
	withEnv(t, "FORCE_COLOR=3")
	th := asciiTheme(false)
	th.Light = true
	m, tt := boundMultichoice(t, th, 10, []any{"a", "b"})
	m.handleKey(' ')
	got := mframe(t, m, tt)
	// focus and checked marks take the accent, the unchecked mark is muted
	roles := m.look.roles
	assert.True(t, len(roles.accent) > 0 && len(roles.muted) > 0)
	assert.Contains(t, got, string(roles.accent)+">")
	assert.Contains(t, got, string(roles.accent)+"x")
	assert.Contains(t, got, string(roles.muted)+"o")
}

// frameStaysOnScreen replays out, the output of a prompt over a, b, c, d, e
// on a terminal of six rows that confirmed c, on a bounded screen: the first
// frame shows the label at the top, no redraw scrolls a row away, and cleanup
// leaves only the answer.
func frameStaysOnScreen(t *testing.T, out string) {
	t.Helper()
	first := newBoundedScreen(60, 6)
	first.write(out[:strings.Index(out, "\x1b[")])
	assert.Equal(t, "? Fruit", first.visible()[0])
	assert.Equal(t, 0, len(first.scrolled))

	all := newBoundedScreen(60, 6)
	all.write(out)
	assert.Equal(t, 0, len(all.scrolled))
	assert.Equal(t, []string{"+ Fruit · c"}, all.visible())
}

func TestThemedMultichoiceFullHeightFrameKeepsLabelOnScreen(t *testing.T) {
	o, out, _ := themedMultichoiceOpt(t, asciiTheme(false), "\x1b[B", "\x1b[B", " ", "\x1b[A", "\x1b[B", "\r")
	six := opT(func(m *multichoice) error {
		next := m.makeTermIO
		m.makeTermIO = func(in io.Reader, out io.Writer) (*termIO, error) {
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
