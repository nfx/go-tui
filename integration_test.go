// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/assert"
	"golang.org/x/term"
)

func TestDropdownDefaultSelectsClosestMatch(t *testing.T) {
	term := newTerminal(t, 12)
	res := prompt(func() (string, error) {
		return tui.Dropdown(t.Context(), "Pick", []string{"apple pie", "banana", "apple"},
			plain, tui.WithDefault("apple"))
	})
	// two items share the prefix, so the closest one is ranked first and selected
	term.waitRows(t, "Pick * apple", "- apple pie", "- banana")
	term.press(t, keyEnter)
	assert.Equal(t, "apple", answer(t, res))
	term.waitRows(t, "Pick: apple")
}

func TestDropdownLazyDefaultSelectsBestRankedArrival(t *testing.T) {
	term := newTerminal(t, 12)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", feed(items), plain, tui.WithDefault("cat"))
	})
	read := term.blockedRead(t)
	items <- "xxxx"
	term.waitRows(t, "Pick * xxxx")
	items <- "yyyy"
	term.waitRows(t, "Pick * xxxx", "- yyyy")
	// the closest match arrives last: it is ranked first, and without any
	// navigation the selection follows the ranking
	items <- "bat"
	term.waitRows(t, "Pick * bat", "- xxxx", "- yyyy")
	close(items)
	read <- keyEnter
	assert.Equal(t, "bat", answer(t, res))
}

func TestDropdownLazyDefaultKeepsNavigatedSelection(t *testing.T) {
	term := newTerminal(t, 12)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", feed(items), plain, tui.WithDefault("cat"))
	})
	items <- "xxxx"
	items <- "yyyy"
	term.waitRows(t, "Pick * xxxx", "- yyyy")
	term.press(t, keyDown)
	term.waitRows(t, "Pick - xxxx", "* yyyy")
	read := term.blockedRead(t)
	// once the user has moved, a better-ranked arrival does not take the selection
	items <- "bat"
	term.waitRows(t, "Pick - bat", "- xxxx", "* yyyy")
	close(items)
	read <- keyEnter
	assert.Equal(t, "yyyy", answer(t, res))
}

func TestDropdownLazyFilterAppliesToLaterItems(t *testing.T) {
	term := newTerminal(t, 12)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", feed(items), plain)
	})
	items <- "alpha"
	items <- "beta"
	term.waitRows(t, "Pick * alpha", "- beta")
	term.press(t, "a")
	term.waitRows(t, "Pick * alpha")
	items <- "banana"
	items <- "apricot"
	term.waitRows(t, "Pick * alpha", "- apricot")
	term.press(t, keyBackspace)
	term.waitRows(t, "Pick * alpha", "- beta", "- banana", "- apricot")
	term.press(t, "a")
	term.waitRows(t, "Pick * alpha", "- apricot")
	term.press(t, keyDown)
	term.waitRows(t, "Pick - alpha", "* apricot")
	close(items)
	term.press(t, keyEnter)
	assert.Equal(t, "apricot", answer(t, res))
}

// testResizeWhileReading moves the selection, then shrinks the terminal while
// the prompt is blocked reading the next key. The prompt must redraw for the
// new height, keep the selected item, and receive the key from that same read.
func testResizeWhileReading(t *testing.T, run func() (string, error)) {
	t.Helper()
	term := newTerminal(t, 12)
	res := prompt(run)
	term.waitRows(t, "Pick * a", "- b", "- c", "- d", "- e")
	term.press(t, keyDown)
	term.waitRows(t, "Pick - a", "* b", "- c", "- d", "- e")
	term.press(t, keyDown)
	term.waitRows(t, "Pick - a", "- b", "* c", "- d", "- e")
	read := term.blockedRead(t)
	term.resize(t, 40, 4) // room for two items
	term.waitRows(t, "Pick - b", "* c", "~ 2 more")
	// a second reader would take this key and leave the prompt waiting
	read <- keyEnter
	assert.Equal(t, "c", answer(t, res))
}

func TestDropdownResizeWhileReadingKeepsSelection(t *testing.T) {
	testResizeWhileReading(t, func() (string, error) {
		return tui.Dropdown(t.Context(), "Pick", []string{"a", "b", "c", "d", "e"}, plain)
	})
}

func TestDropdownLazyResizeWhileReadingKeepsSelection(t *testing.T) {
	testResizeWhileReading(t, func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", values("a", "b", "c", "d", "e"), plain)
	})
}

func TestWidgetsShareTerminal(t *testing.T) {
	term := newTerminal(t, 12)
	p, err := tui.NewMaxProgressBar(t.Context(), "Download", 4)
	assert.NoError(t, err)
	p.Add(1)
	term.waitRows(t, "Download 25%...")

	res := prompt(func() (string, error) {
		return tui.Dropdown(t.Context(), "Pick", []string{"a", "b"}, plain)
	})
	// the background overlay stays above the prompt that started later
	term.waitRows(t, "Download 25%...", "Pick * a", "- b")

	// log output goes above both overlays
	_, err = fmt.Fprintln(tui.Stderr(), "log line")
	assert.NoError(t, err)
	term.waitRows(t, "log line", "Download 25%...", "Pick * a", "- b")

	term.press(t, keyEnter)
	assert.Equal(t, "a", answer(t, res))
	term.waitRows(t, "log line", "Pick: a", "Download 25%...")

	p.Add(3) // completes the bar, which then disappears
	term.waitRows(t, "log line", "Pick: a")
	assert.NoError(t, p.Close())

	// the next prompt reads from the same keyboard and draws on the same screen
	res = prompt(func() (string, error) {
		return tui.Input(t.Context(), "Name")
	})
	term.press(t, "x")
	term.press(t, keyEnter)
	assert.Equal(t, "x", answer(t, res))
	term.waitRows(t, "log line", "Pick: a", "✔ Name … x")
}

type scoredRow struct {
	Name  string
	Score int
}

var scoreCell = regexp.MustCompile(`(\x1b\[[0-9;]*m)?(\d+) *(?:\x1b\[0m)? *$`)

// scoreColors maps every score in a rendered table to the color of its cell.
func scoreColors(t *testing.T, rows []scoredRow) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	assert.NoError(t, tui.TableAuto(&buf, rows, tui.WithColumnGreenRedScale("Score")))
	colors := map[string]string{}
	for line := range strings.SplitSeq(buf.String(), "\n") {
		m := scoreCell.FindStringSubmatch(line)
		if m != nil {
			colors[m[2]] = m[1]
		}
		// an empty cell is never colored
		assert.NotContains(t, line, "m\x1b[0m")
	}
	assert.Equal(t, len(rows), len(colors))
	return colors
}

func TestTableAutoScaleColorsFollowMultilineRows(t *testing.T) {
	scores := []int{7, 3, 11, 1, 9, 5, 12, 2, 8, 4, 10, 6}
	var single, multiline []scoredRow
	for i, score := range scores {
		name := strings.Repeat(string(rune('a'+i)), 3)
		single = append(single, scoredRow{name, score})
		if i == 3 || i == 10 { // in both the first and the second batch
			name += "\ncontinued"
		}
		multiline = append(multiline, scoredRow{name, score})
	}
	// a value with a newline does not change which color any score gets
	assert.Equal(t, scoreColors(t, single), scoreColors(t, multiline))
}

func TestProgressbarUpdatesAfterCompletion(t *testing.T) {
	term := newTerminal(t, 6)
	p, err := tui.NewMaxProgressBar(t.Context(), "Copy", 4)
	assert.NoError(t, err)
	p.Add(2)
	term.waitRows(t, "Copy 50%...")
	p.Add(5) // overshoots the maximum
	// the bar completes on its next redraw and disappears
	term.waitRows(t)
	updated := make(chan struct{})
	go func() {
		defer close(updated)
		p.Add(1)
		p.Add(1)
	}()
	select {
	case <-updated:
	case <-time.After(failsafe):
		t.Fatal("updates block after completion")
	}
	assert.NoError(t, p.Close())
	assert.NoError(t, p.Close())
	p.Add(1)
	_, err = fmt.Fprintln(tui.Stderr(), "after")
	assert.NoError(t, err)
	term.waitRows(t, "after")
}

func TestSpinnerUpdatesAfterCompletion(t *testing.T) {
	term := newTerminal(t, 6)
	s, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	t.Cleanup(s.Close)
	frames := tui.WithFrames([]string{"-"})
	failed, err := s.Add(t.Context(), tui.WithPrefixf("fetch"), frames)
	assert.NoError(t, err)
	kept, err := s.Add(t.Context(), tui.WithPrefixf("build"), frames, tui.WithKeep())
	assert.NoError(t, err)
	failed.Fail(errors.New("timeout"))
	kept.Update("done")
	assert.NoError(t, kept.Close())
	// updates after completion do not change what was reported
	failed.Update("retrying")
	kept.Update("restarted")
	// spinners apply changes in order, so a frame with this one has all of them
	_, err = s.Add(context.Background(), tui.WithPrefixf("sentinel"), frames)
	assert.NoError(t, err)
	term.waitRows(t, "- fetch: timeout", "- build: done", "- sentinel:")
}

// failsafe turns a hang into a failure; it never orders events.
const failsafe = 5 * time.Second

// keyboardFd is never a terminal, so prompts on the keyboard skip raw mode.
const keyboardFd = 1 << 20

const (
	keyEnter     = "\r"
	keyDown      = "\x1b[B"
	keyBackspace = "\x7f"
)

// keyboard is the input of the terminal. Every Read blocks until the test
// completes it, so a prompt waiting for a key is an observable state.
type keyboard struct {
	reads  chan chan<- string
	closed chan struct{}
}

func newKeyboard(t *testing.T) *keyboard {
	t.Helper()
	k := &keyboard{
		reads:  make(chan chan<- string),
		closed: make(chan struct{}),
	}
	t.Cleanup(func() { close(k.closed) })
	return k
}

func (k *keyboard) Fd() uintptr {
	return keyboardFd
}

// Read returns one key per call, as a terminal in raw mode does.
func (k *keyboard) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	reply := make(chan string, 1)
	select {
	case <-k.closed:
		return 0, io.EOF
	case k.reads <- reply:
	}
	select {
	case <-k.closed:
		return 0, io.EOF
	case key := <-reply:
		if len(key) > len(p) {
			panic(fmt.Sprintf("key %q does not fit into a read of %d bytes", key, len(p)))
		}
		return copy(p, key), nil
	}
}

// blockedRead waits until a prompt is blocked reading a key and returns that
// read, so that the test decides when and with which key it completes.
func (k *keyboard) blockedRead(t *testing.T) chan<- string {
	t.Helper()
	select {
	case read := <-k.reads:
		return read
	case <-time.After(failsafe):
		t.Fatal("no prompt is reading a key")
		return nil
	}
}

// press completes the next blocked read with key.
func (k *keyboard) press(t *testing.T, key string) {
	t.Helper()
	k.blockedRead(t) <- key
}

// screen applies terminal output to an unbounded grid of cells, so that tests
// see what a user would see. Rows below the bottom are added instead of
// scrolling.
type screen struct {
	mu       sync.Mutex
	width    int
	cells    [][]rune
	row, col int
	wrap     bool   // the last column is written, the next rune goes on the next row
	esc      []byte // an escape sequence that is not complete yet
	changed  chan struct{}
}

func newScreen(width int) *screen {
	return &screen{width: width, changed: make(chan struct{})}
}

// Write applies one write of the arbiter, which is always a complete redraw.
func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < len(p); {
		b := p[i]
		switch {
		case len(s.esc) > 0:
			s.escape(b)
		case b == 0x1b:
			s.esc = []byte{b}
		case b == '\r':
			s.col, s.wrap = 0, false
		case b == '\n':
			s.row, s.wrap = s.row+1, false
		case b < 0x20:
		default:
			r, size := utf8.DecodeRune(p[i:])
			s.put(r)
			i += size
			continue
		}
		i++
	}
	close(s.changed)
	s.changed = make(chan struct{})
	return len(p), nil
}

func (s *screen) line(row int) []rune {
	for len(s.cells) <= row {
		s.cells = append(s.cells, []rune(strings.Repeat(" ", s.width)))
	}
	return s.cells[row]
}

func (s *screen) put(r rune) {
	if s.wrap {
		s.row, s.col, s.wrap = s.row+1, 0, false
	}
	s.line(s.row)[s.col] = r
	if s.col == s.width-1 {
		s.wrap = true
	} else {
		s.col++
	}
}

func (s *screen) escape(b byte) {
	s.esc = append(s.esc, b)
	if len(s.esc) == 2 && b != '[' {
		s.esc = nil // a two-byte escape, ignored
		return
	}
	if len(s.esc) < 3 || b < 0x40 || b > 0x7e {
		return
	}
	params := strings.TrimPrefix(string(s.esc[2:len(s.esc)-1]), "?")
	s.esc = nil
	n, err := strconv.Atoi(strings.Split(params, ";")[0])
	if err != nil {
		n = 0
	}
	s.wrap = false
	switch b {
	case 'A':
		s.row = max(0, s.row-max(1, n))
	case 'B':
		s.row += max(1, n)
	case 'C':
		s.col = min(s.width-1, s.col+max(1, n))
	case 'D':
		s.col = max(0, s.col-max(1, n))
	case 'K':
		line := s.line(s.row)
		from, to := s.col, len(line)
		switch n {
		case 1:
			from, to = 0, s.col+1
		case 2:
			from = 0
		}
		for i := from; i < to; i++ {
			line[i] = ' '
		}
	}
}

// rows returns the rows that are not blank, without surrounding spaces.
func (s *screen) rows() (rows []string) {
	for _, line := range s.cells {
		if row := strings.TrimSpace(string(line)); row != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

// waitRows waits until the screen shows exactly the given rows. A wanted row
// ending with "..." only has to start with what precedes it.
func (s *screen) waitRows(t *testing.T, want ...string) {
	t.Helper()
	timeout := time.After(failsafe)
	for {
		s.mu.Lock()
		rows, changed := s.rows(), s.changed
		s.mu.Unlock()
		if rowsMatch(rows, want) {
			return
		}
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("timed out waiting for rows %q, screen shows %q", want, rows)
		}
	}
}

func rowsMatch(rows, want []string) bool {
	if len(rows) != len(want) {
		return false
	}
	for i, w := range want {
		prefix, ok := strings.CutSuffix(w, "...")
		if ok && !strings.HasPrefix(rows[i], prefix) || !ok && rows[i] != w {
			return false
		}
	}
	return true
}

// ttyWriter is the terminal output: it is a real terminal to check and to
// measure, but each write goes to the screen in one piece.
type ttyWriter struct {
	fd     uintptr
	screen *screen
}

func (w *ttyWriter) Write(p []byte) (int, error) {
	return w.screen.Write(p)
}

func (w *ttyWriter) Fd() uintptr {
	return w.fd
}

type terminal struct {
	*keyboard
	*screen
	tty *os.File
}

// newTerminal makes a terminal of the given size the package default for
// input and output, like the stdin and stderr of a CLI.
func newTerminal(t *testing.T, height int) *terminal {
	t.Helper()
	const width = 40
	tty := openTTY(t, width, height)
	term := &terminal{keyboard: newKeyboard(t), screen: newScreen(width), tty: tty}
	tui.SetDefaultIO(term.keyboard, &ttyWriter{fd: tty.Fd(), screen: term.screen})
	t.Cleanup(func() {
		closer, ok := tui.Stderr().(io.Closer)
		if ok {
			assert.NoError(t, closer.Close())
		}
		tui.SetDefaultIO(os.Stdin, os.Stderr)
	})
	return term
}

func (term *terminal) resize(t *testing.T, width, height int) {
	t.Helper()
	resizeTTY(t, term.tty, width, height)
}

// plain renders dropdowns without colors: the active item is marked with "*"
// and the others with "-".
var plain = tui.WithOptions(
	tui.WithLabelTemplate("{{ . }}"),
	tui.WithActiveItemTemplate("* {{ . }}"),
	tui.WithInactiveItemTemplate("- {{ . }}"),
	tui.WithMoreItemsTemplate("~ {{ .More }} more"),
	tui.WithAnswerTemplate("{{ .Label }}: {{ .Answer }}"),
)

type promptResult struct {
	value string
	err   error
}

// prompt runs fn in the background, like a CLI waiting on its user.
func prompt(fn func() (string, error)) <-chan promptResult {
	res := make(chan promptResult, 1)
	go func() {
		value, err := fn()
		res <- promptResult{value, err}
	}()
	return res
}

func answer(t *testing.T, res <-chan promptResult) string {
	t.Helper()
	select {
	case r := <-res:
		assert.NoError(t, r.err)
		return r.value
	case <-time.After(failsafe):
		t.Fatal("prompt did not return")
		return ""
	}
}

// feed returns an iterator that yields the items the test sends on ch.
func feed(ch <-chan string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for v := range ch {
			if !yield(v, nil) {
				return
			}
		}
	}
}

func values(items ...string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for _, v := range items {
			if !yield(v, nil) {
				return
			}
		}
	}
}

func TestMultichoicePublic(t *testing.T) {
	for _, tc := range []struct {
		name, input, hint string
		items, want       []string
	}{
		{"none", "\r", "", []string{"one"}, []string{}},
		{"enter does not check", "\x1b[B\r", "", []string{"one", "two"}, []string{}},
		{"duplicate identities", " \x1b[B \r", "", []string{"api", "api", "worker"}, []string{"api", "api"}},
		{"toggle twice", "  \r", "", []string{"one"}, []string{}},
		{"all", " \x1b[B \x1b[B \r", "", []string{"one", "two", "three"}, []string{"one", "two", "three"}},
		{"reverse order", "\x1b[B \x1b[A \r", "", []string{"one", "two"}, []string{"one", "two"}},
		{"unique prefix", "\r", "BE", []string{"alpha", "Beta"}, []string{"Beta"}},
		{"ambiguous exact", "\r", "api", []string{"api", "api server"}, []string{}},
		{"duplicate default", "\r", "api", []string{"api", "api"}, []string{}},
		{"unmatched default", "\r", "zz", []string{"one", "two"}, []string{}},
		{"uncheck default once", " z\x1b\r", "one", []string{"one", "two"}, []string{}},
		{"hidden selected", " z \r", "", []string{"one", "two"}, []string{"one"}},
		{"recover no matches", "z\x7f \r", "", []string{"one", "two"}, []string{"one"}},
		{"escape clears", "z\x1b\x7f \r", "", []string{"one", "two"}, []string{"one"}},
		{"ranked source order", " \x1b[B \r", "b", []string{"alpha", "beta", "bravo"}, []string{"beta", "bravo"}},
		{"control keys ignored", "\t\x1b[3~\x1bx \r", "", []string{"one"}, []string{"one"}},
		{"paste text not keys", "\x1b[200~tw\n\x03\x1b[201~ \r", "", []string{"one", "two"}, []string{"two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tui.Multichoice(
				t.Context(),
				"Pick",
				tc.items,
				tui.WithInput(strings.NewReader(tc.input)),
				tui.WithOutput(io.Discard),
				tui.WithHide(),
				tui.WithDefault(tc.hint),
			)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func TestMultichoiceOptionsAndTemplates(t *testing.T) {
	type service struct{ Name, Region string }
	items := []service{{"api", "eu"}, {"worker", "us"}}
	var out bytes.Buffer
	got, err := tui.Multichoice(t.Context(), "Services", items,
		tui.WithInput(strings.NewReader("\r")), tui.WithOutput(&out),
		tui.WithOptions(tui.WithOneReturn(), tui.WithTemplate(".Name", ".Region"), tui.WithDefault("worker")),
		tui.WithAnswerTemplate(`ANSWER {{.Count}}{{range .Answer}} {{.Name}}/{{.Region}}{{else}} none{{end}}`))
	if err != nil || !reflect.DeepEqual(got, items[1:]) {
		t.Fatalf("got %v, %v", got, err)
	}
	if strings.Count(out.String(), "ANSWER 1 worker/us") != 1 || !strings.Contains(out.String(), "[x]") {
		t.Fatal(out.String())
	}
	got[0].Name = "changed"
	if items[1].Name != "worker" {
		t.Fatal("result aliases source slice")
	}
	t.Run("generated collection answer", func(t *testing.T) {
		out.Reset()
		_, err := tui.Multichoice(
			t.Context(),
			"Services",
			items,
			tui.WithTemplate(".Name", ".Region"),
			tui.WithDefault("api"),
			tui.WithInput(strings.NewReader("\r")),
			tui.WithOutput(&out),
		)
		if err != nil || !strings.Contains(out.String(), "✔ Services") || !strings.Contains(out.String(), "api") {
			t.Fatalf("%s: %v", out.String(), err)
		}
	})
	t.Run("deprecated fields", func(t *testing.T) {
		_, err := tui.Multichoice(
			t.Context(),
			"Services",
			items,
			tui.WithFieldTemplate("Name", "Region"),
			tui.WithDefault("us"),
			tui.WithInput(strings.NewReader("\r")),
			tui.WithOutput(io.Discard),
		)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("last hint and clear", func(t *testing.T) {
		got, err := tui.Multichoice(
			t.Context(),
			"Services",
			items,
			tui.WithTemplate(".Name"),
			tui.WithDefault("api"),
			tui.WithDefault("worker"),
			tui.WithDefault(""),
			tui.WithInput(strings.NewReader("\r")),
			tui.WithOutput(io.Discard),
		)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("%v, %v", got, err)
		}
	})
	t.Run("nil items", func(t *testing.T) {
		got, err := tui.Multichoice(
			t.Context(),
			"Nil",
			[]any{nil, (*service)(nil), []int{1}},
			tui.WithInput(strings.NewReader(" \x1b[B \x1b[B \r")),
			tui.WithOutput(io.Discard),
		)
		if err != nil || len(got) != 3 {
			t.Fatalf("%#v, %v", got, err)
		}
	})
}

func TestMultichoiceMapProjections(t *testing.T) {
	values := map[string][]int{"beta": {1}, "alpha": {1}, "gamma": {3}}
	var out bytes.Buffer
	keys, err := tui.MultichoiceKVK(
		t.Context(),
		"Keys",
		values,
		tui.WithDefault("b"),
		tui.WithInput(strings.NewReader("\x1b[B \r")),
		tui.WithOutput(&out),
		tui.WithAnswerTemplate(`ANSWER {{.Count}}{{range .Answer}} {{.Key}}={{index .Value 0}}{{end}}`),
	)
	if err != nil || !reflect.DeepEqual(keys, []string{"alpha", "beta"}) {
		t.Fatalf("%v, %v", keys, err)
	}
	if strings.Count(out.String(), "ANSWER 2 alpha=1 beta=1") != 1 {
		t.Fatal(out.String())
	}
	out.Reset()
	selected, err := tui.MultichoiceKVV(
		t.Context(),
		"Values",
		values,
		tui.WithDefault("b"),
		tui.WithInput(strings.NewReader("\x1b[B \r")),
		tui.WithOutput(&out),
		tui.WithAnswerTemplate(`ANSWER {{.Count}}{{range .Answer}} {{.Key}}={{index .Value 0}}{{end}}`),
	)
	if err != nil || !reflect.DeepEqual(selected, [][]int{{1}, {1}}) {
		t.Fatalf("%v, %v", selected, err)
	}
	if strings.Count(out.String(), "ANSWER 2 alpha=1 beta=1") != 1 {
		t.Fatal(out.String())
	}
	type payload struct{ Name string }
	result, err := tui.MultichoiceKVV(
		t.Context(),
		"Custom",
		map[string]payload{"key": {Name: "display"}},
		tui.WithTemplate(".Value.Name"),
		tui.WithDefault("dis"),
		tui.WithInput(strings.NewReader("\r")),
		tui.WithOutput(io.Discard),
	)
	if err != nil || !reflect.DeepEqual(result, []payload{{"display"}}) {
		t.Fatalf("%v, %v", result, err)
	}
	empty, err := tui.MultichoiceKVV(
		t.Context(),
		"None",
		values,
		tui.WithInput(strings.NewReader("\r")),
		tui.WithOutput(io.Discard),
	)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("%v, %v", empty, err)
	}
}

func TestMultichoiceErrors(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []string
		err         error
	}{
		{"EOF", "", nil, io.EOF},
		{"default must confirm", "", nil, io.EOF},
		{"ctrl C", "\x03", nil, io.EOF},
		{"ctrl D", "\x04", nil, io.EOF},
		{"incomplete paste", "\x1b[200~one", nil, io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tui.Multichoice(
				t.Context(),
				"Pick",
				[]string{"one", "two"},
				tui.WithInput(strings.NewReader(tc.input)),
				tui.WithOutput(io.Discard),
			)
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%#v, %v; want %#v, %v", got, err, tc.want, tc.err)
			}
		})
	}
	_, err := tui.Multichoice(t.Context(), "Empty", []string(nil))
	if !errors.Is(err, tui.ErrNoItems) {
		t.Fatal(err)
	}
	_, err = tui.MultichoiceKVK(t.Context(), "Empty", map[int]int{})
	if !errors.Is(err, tui.ErrNoItems) {
		t.Fatal(err)
	}
	got, err := tui.Multichoice(t.Context(), "Wrong", []string{"one"}, tui.WithOneReturn())
	if got != nil || !errors.Is(err, tui.ErrWrongWidget) {
		t.Fatal(got, err)
	}
	got, err = tui.Multichoice(t.Context(), "Bad", []string{"one"}, tui.WithInactiveItemTemplate("{{"))
	if got != nil || err == nil {
		t.Fatal(got, err)
	}
	got, err = tui.Multichoice(t.Context(), "Bad", []string{"one"}, tui.WithInactiveItemTemplate("{{.Missing}}"))
	if got != nil || err == nil {
		t.Fatal(got, err)
	}
	var out bytes.Buffer
	got, err = tui.Multichoice(
		t.Context(),
		"Bad",
		[]string{"one"},
		tui.WithAnswerTemplate("{{.Missing}}"),
		tui.WithInput(strings.NewReader(" \r")),
		tui.WithOutput(&out),
	)
	if got != nil || err == nil {
		t.Fatal(got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err = tui.Multichoice(ctx, "Canceled", []string{"one"}, tui.WithOutput(&out))
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
}

func TestMultichoiceSequentialReaders(t *testing.T) {
	in := strings.NewReader(" \r \rnext\rsecret\r\r")
	first, err := tui.Multichoice(t.Context(), "First", []string{"one"}, tui.WithInput(in), tui.WithOutput(io.Discard))
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	second, err := tui.Multichoice(
		t.Context(),
		"Second",
		[]string{"two"},
		tui.WithInput(in),
		tui.WithOutput(io.Discard),
	)
	if err != nil || len(second) != 1 {
		t.Fatal(second, err)
	}
	value, err := tui.Input(t.Context(), "Text", tui.WithInput(in), tui.WithOutput(io.Discard))
	if err != nil || value != "next" {
		t.Fatal(value, err)
	}
	secret, err := tui.Password(t.Context(), "Secret", tui.WithInput(in), tui.WithOutput(io.Discard))
	if err != nil || secret != "secret" {
		t.Fatal(secret, err)
	}
	_, err = tui.Dropdown(t.Context(), "Last", []string{"one", "two"}, tui.WithInput(in), tui.WithOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
}

func ExampleMultichoice() {
	selected, err := tui.Multichoice(
		context.Background(),
		"Instances",
		[]string{"api", "api", "worker"},
		tui.WithInput(strings.NewReader(" \x1b[B \r")),
		tui.WithOutput(io.Discard),
		tui.WithHide(),
	)
	fmt.Println(selected, err)
	// Output: [api api] <nil>
}

func ExampleMultichoiceKVK() {
	selected, err := tui.MultichoiceKVK(
		context.Background(),
		"Protocols",
		map[string]int{"http": 80, "https": 443},
		tui.WithDefault("https"),
		tui.WithInput(strings.NewReader("\r")),
		tui.WithOutput(io.Discard),
	)
	fmt.Println(selected, err)
	// Output: [https] <nil>
}

func ExampleMultichoiceKVV() {
	selected, err := tui.MultichoiceKVV(
		context.Background(),
		"Protocols",
		map[string]int{"http": 80, "https": 443},
		tui.WithDefault("https"),
		tui.WithInput(strings.NewReader("\r")),
		tui.WithOutput(io.Discard),
	)
	fmt.Println(selected, err)
	// Output: [443] <nil>
}

var checklistPlain = tui.WithOptions(
	tui.WithLabelTemplate("{{.}}"),
	tui.WithActiveItemTemplate("{{.}}"),
	tui.WithInactiveItemTemplate("{{.}}"),
	tui.WithMoreItemsTemplate(
		"~ {{.More}} more",
	),
	tui.WithAnswerTemplate(`{{.Label}}:{{range .Answer}} {{.}}{{else}} none{{end}}`),
)

func TestMultichoiceSharesTerminalWithProgressAndLogs(t *testing.T) {
	term := newTerminal(t, 12)
	progress, err := tui.NewMaxProgressBar(t.Context(), "Download", 4)
	if err != nil {
		t.Fatal(err)
	}
	progress.Add(1)
	term.waitRows(t, "Download 25%...")
	res := prompt(func() (string, error) {
		values, err := tui.Multichoice(t.Context(), "Pick", []string{"a", "b"}, checklistPlain)
		return fmt.Sprint(values), err
	})
	term.waitRows(t, "Download 25%...", "Pick", "→ [ ] a", "[ ] b")
	if _, err := fmt.Fprintln(tui.Stderr(), "log line"); err != nil {
		t.Fatal(err)
	}
	term.waitRows(t, "log line", "Download 25%...", "Pick", "→ [ ] a", "[ ] b")
	term.press(t, " ")
	term.press(t, keyDown)
	term.waitRows(t, "log line", "Download 25%...", "Pick", "[x] a", "→ [ ] b")
	term.press(t, keyEnter)
	if got := answer(t, res); got != "[a]" {
		t.Fatal(got)
	}
	term.waitRows(t, "log line", "Pick: a", "Download 25%...")
	progress.Add(3)
	if err := progress.Close(); err != nil {
		t.Fatal(err)
	}
	term.waitRows(t, "log line", "Pick: a")
	res = prompt(func() (string, error) { return tui.Input(t.Context(), "Next", tui.WithHide()) })
	term.press(t, "x")
	term.press(t, keyEnter)
	if got := answer(t, res); got != "x" {
		t.Fatal(got)
	}
}

func TestMultichoiceLazyManagedFilterAndConfirmation(t *testing.T) {
	term := newTerminal(t, 12)
	items := make(chan string)
	res := prompt(func() (string, error) {
		values, err := tui.MultichoiceLazy(t.Context(), "Pick", feed(items), checklistPlain, tui.WithDefault("api"))
		return fmt.Sprint(values), err
	})
	term.waitRows(t, "Pick")
	items <- "api"
	term.waitRows(t, "Pick", "→ [ ] api")
	term.press(t, "z")
	term.waitRows(t, "Pick", "No matches")
	items <- "zzz"
	term.waitRows(t, "Pick", "→ [ ] zzz")
	term.press(t, " ")
	term.press(t, keyEnter)
	term.waitRows(t, "Pick", "→ [x] zzz")
	read := term.blockedRead(t)
	close(items)
	read <- keyBackspace
	term.waitRows(t, "Pick", "[x] api", "→ [x] zzz")
	term.press(t, keyEnter)
	if got := answer(t, res); got != "[api zzz]" {
		t.Fatal(got)
	}
	term.waitRows(t, "Pick: api zzz")
}

func TestMultichoiceManagedResizePreservesIdentity(t *testing.T) {
	term := newTerminal(t, 12)
	res := prompt(func() (string, error) {
		values, err := tui.Multichoice(t.Context(), "Pick", []string{"a", "b", "c", "d", "e"}, checklistPlain)
		return fmt.Sprint(values), err
	})
	term.waitRows(t, "Pick", "→ [ ] a", "[ ] b", "[ ] c", "[ ] d", "[ ] e")
	term.press(t, keyDown)
	term.press(t, keyDown)
	term.press(t, " ")
	read := term.blockedRead(t)
	term.resize(t, 40, 4)
	term.waitRows(t, "Pick", "[ ] b", "→ [x] c", "~ 2 more")
	term.resize(t, 40, 12)
	term.waitRows(t, "Pick", "[ ] a", "[ ] b", "→ [x] c", "[ ] d", "[ ] e")
	read <- keyEnter
	if got := answer(t, res); got != "[c]" {
		t.Fatal(got)
	}
}

func TestMultichoiceManagedCancelAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(strconv.FormatBool(deadline), func(t *testing.T) {
			term := newTerminal(t, 12)
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 750*time.Millisecond)
			}
			defer cancel()
			res := prompt(func() (string, error) {
				values, err := tui.Multichoice(ctx, "Pick", []string{"a"}, checklistPlain)
				return fmt.Sprint(values), err
			})
			term.waitRows(t, "Pick", "→ [ ] a")
			read := term.blockedRead(t)
			if !deadline {
				cancel()
			}
			select {
			case got := <-res:
				want := context.Canceled
				if deadline {
					want = context.DeadlineExceeded
				}
				if !errors.Is(got.err, want) {
					t.Fatal(got.err)
				}
			case <-time.After(failsafe):
				t.Fatal("cancel blocked")
			}
			term.waitRows(t)
			next := prompt(func() (string, error) { return tui.Input(t.Context(), "Next", tui.WithHide()) })
			read <- "x\r"
			if got := answer(t, next); got != "x" {
				t.Fatal(got)
			}
		})
	}
}

// The writer-driven read starts after the completed lazy frame. It makes the
// executable example deterministic without assuming producer scheduling.
type lazyExampleIO struct {
	ready chan struct{}
	sent  bool
}

// Write releases confirmation only after the lazy default is visibly checked.
func (s *lazyExampleIO) Write(p []byte) (int, error) {
	if !s.sent && strings.Contains(string(p), "[x] api") {
		s.sent = true
		close(s.ready)
	}
	return len(p), nil
}

// Read supplies Enter after the completed default selection is rendered.
func (s *lazyExampleIO) Read(p []byte) (int, error) { <-s.ready; p[0] = '\r'; return 1, nil }

func ExampleMultichoiceLazy() {
	stream := &lazyExampleIO{ready: make(chan struct{})}
	selected, err := tui.MultichoiceLazy(
		context.Background(),
		"Components",
		values("api", "worker"),
		tui.WithDefault("api"),
		tui.WithInput(stream),
		tui.WithOutput(stream),
		tui.WithHide(),
	)
	fmt.Println(selected, err)
	// Output: [api] <nil>
}

var _ io.ReadWriter = (*lazyExampleIO)(nil)

func TestMultichoicePTYKeyboardResizeAndRestore(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirm", true: "cancel"}[abort], func(t *testing.T) {
			master, tty, err := openPty()
			if err != nil {
				t.Skipf("no pseudo-terminal: %v", err)
			}
			defer func() { assert.NoError(t, master.Close()) }()
			defer func() { assert.NoError(t, tty.Close()) }()
			setTTYSize(t, tty, 40, 12)
			before, err := term.GetState(int(tty.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			screen := newScreen(40)
			tui.SetDefaultIO(tty, &ttyWriter{fd: tty.Fd(), screen: screen})
			defer func() {
				if closer, ok := tui.Stderr().(io.Closer); ok {
					if err := closer.Close(); err != nil {
						t.Error(err)
					}
				}
				tui.SetDefaultIO(os.Stdin, os.Stderr)
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			selected := make(chan []string, 1)
			go func() {
				got, err := tui.Multichoice(ctx, "Pick", []string{"a", "b", "c", "d", "e"}, checklistPlain)
				selected <- got
				result <- err
			}()
			screen.waitRows(t, "Pick", "→ [ ] a", "[ ] b", "[ ] c", "[ ] d", "[ ] e")
			if _, err := master.WriteString("\x1b[B\x1b[B "); err != nil {
				t.Fatal(err)
			}
			screen.waitRows(t, "Pick", "[ ] a", "[ ] b", "→ [x] c", "[ ] d", "[ ] e")
			resizeTTY(t, tty, 40, 4)
			screen.waitRows(t, "Pick", "[ ] b", "→ [x] c", "~ 2 more")
			if abort {
				cancel()
			} else {
				if _, err := master.WriteString("\r"); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if abort && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if !abort && err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("PTY prompt did not finish")
			}
			got := <-selected
			if abort && got != nil {
				t.Fatal(got)
			}
			if !abort && !reflect.DeepEqual(got, []string{"c"}) {
				t.Fatal(got)
			}
			after, err := term.GetState(int(tty.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("terminal mode not restored")
			}
		})
	}
}
