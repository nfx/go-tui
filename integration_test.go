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
	"slices"
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
	term := newASCIITerminal(t)
	res := prompt(func() (string, error) {
		return tui.Dropdown(t.Context(), "Pick", []string{"apple pie", "banana", "apple"},
			tui.WithDefault("apple"))
	})
	// two items share the prefix, so the closest one is ranked first and selected
	term.waitRows(t, "? Pick", "| > apple", "|   apple pie", "|   banana", "| up/down move...")
	term.press(t, keyEnter)
	assert.Equal(t, "apple", answer(t, res))
	term.waitRows(t, "+ Pick · apple")
}

func TestDropdownLazyDefaultSelectsBestRankedArrival(t *testing.T) {
	term := newASCIITerminal(t)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", feed(items), tui.WithDefault("cat"))
	})
	read := term.blockedRead(t)
	items <- "xxxx"
	term.waitRows(t, "? Pick", "| > xxxx", "| up/down move...")
	items <- "yyyy"
	term.waitRows(t, "? Pick", "| > xxxx", "|   yyyy", "| up/down move...")
	// the closest match arrives last: it is ranked first, and without any
	// navigation the selection follows the ranking
	items <- "bat"
	term.waitRows(t, "? Pick", "| > bat", "|   xxxx", "|   yyyy", "| up/down move...")
	close(items)
	read <- keyEnter
	assert.Equal(t, "bat", answer(t, res))
}

func TestDropdownLazyDefaultKeepsNavigatedSelection(t *testing.T) {
	term := newASCIITerminal(t)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", feed(items), tui.WithDefault("cat"))
	})
	items <- "xxxx"
	items <- "yyyy"
	term.waitRows(t, "? Pick", "| > xxxx", "|   yyyy", "| up/down move...")
	term.press(t, keyDown)
	term.waitRows(t, "? Pick", "|   xxxx", "| > yyyy", "| up/down move...")
	read := term.blockedRead(t)
	// once the user has moved, a better-ranked arrival does not take the selection
	items <- "bat"
	term.waitRows(t, "? Pick", "|   bat", "|   xxxx", "| > yyyy", "| up/down move...")
	close(items)
	read <- keyEnter
	assert.Equal(t, "yyyy", answer(t, res))
}

func TestDropdownLazyFilterAppliesToLaterItems(t *testing.T) {
	term := newASCIITerminal(t)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", feed(items))
	})
	items <- "alpha"
	items <- "beta"
	term.waitRows(t, "? Pick", "| > alpha", "|   beta", "| up/down move...")
	term.press(t, "a")
	term.waitRows(t, "? Pick", "| > alpha", "| up/down move...")
	items <- "banana"
	items <- "apricot"
	term.waitRows(t, "? Pick", "| > alpha", "|   apricot", "| up/down move...")
	term.press(t, keyBackspace)
	term.waitRows(t, "? Pick", "| > alpha", "|   beta", "|   banana", "|   apricot", "| up/down move...")
	term.press(t, "a")
	term.waitRows(t, "? Pick", "| > alpha", "|   apricot", "| up/down move...")
	term.press(t, keyDown)
	term.waitRows(t, "? Pick", "|   alpha", "| > apricot", "| up/down move...")
	close(items)
	term.press(t, keyEnter)
	assert.Equal(t, "apricot", answer(t, res))
}

// testResizeWhileReading checks that resizing during a blocked read preserves selection and pending input.
func testResizeWhileReading(t *testing.T, run func() (string, error)) {
	t.Helper()
	term := newASCIITerminal(t)
	res := prompt(run)
	term.waitRows(t, "? Pick", "| > a", "|   b", "|   c", "|   d", "|   e", "| up/down move...")
	term.press(t, keyDown)
	term.waitRows(t, "? Pick", "|   a", "| > b", "|   c", "|   d", "|   e", "| up/down move...")
	term.press(t, keyDown)
	term.waitRows(t, "? Pick", "|   a", "|   b", "| > c", "|   d", "|   e", "| up/down move...")
	read := term.blockedRead(t)
	term.resize(t, 40, 5) // room for two items and the count
	term.waitRows(t, "? Pick", "|   b", "| > c", "| . 2 more")
	// a second reader would take this key and leave the prompt waiting
	read <- keyEnter
	assert.Equal(t, "c", answer(t, res))
}

func TestDropdownResizeWhileReadingKeepsSelection(t *testing.T) {
	testResizeWhileReading(t, func() (string, error) {
		return tui.Dropdown(t.Context(), "Pick", []string{"a", "b", "c", "d", "e"})
	})
}

func TestDropdownLazyResizeWhileReadingKeepsSelection(t *testing.T) {
	testResizeWhileReading(t, func() (string, error) {
		return tui.DropdownLazy(t.Context(), "Pick", values("a", "b", "c", "d", "e"))
	})
}

func TestWidgetsShareTerminal(t *testing.T) {
	term := newASCIITerminal(t)
	p, err := tui.NewMaxProgressBar(t.Context(), "Download", 4)
	assert.NoError(t, err)
	p.Add(1)
	term.waitRows(t, "Download  25%...")

	res := prompt(func() (string, error) {
		return tui.Dropdown(t.Context(), "Pick", []string{"a", "b"})
	})
	// the background overlay stays above the prompt that started later
	term.waitRows(t, "Download  25%...", "? Pick", "| > a", "|   b", "| up/down move...")

	// log output goes above both overlays
	_, err = fmt.Fprintln(tui.Stderr(), "log line")
	assert.NoError(t, err)
	term.waitRows(t, "log line", "Download  25%...", "? Pick", "| > a", "|   b", "| up/down move...")

	term.press(t, keyEnter)
	assert.Equal(t, "a", answer(t, res))
	term.waitRows(t, "log line", "+ Pick · a", "Download  25%...")

	p.Add(3) // completes the bar, which then disappears
	term.waitRows(t, "log line", "+ Pick · a")
	assert.NoError(t, p.Close())

	// the next prompt reads from the same keyboard and draws on the same screen
	res = prompt(func() (string, error) {
		return tui.Input(t.Context(), "Name")
	})
	term.press(t, "x")
	term.press(t, keyEnter)
	assert.Equal(t, "x", answer(t, res))
	term.waitRows(t, "log line", "+ Pick · a", "+ Name · x")
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
	term.waitRows(t, "Copy  50%...")
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
	th := tui.NewTheme()
	th.Symbols.Spinner = []string{"-"}
	tui.UseTheme(t, th)
	s, err := tui.NewSpinners(t.Context())
	assert.NoError(t, err)
	t.Cleanup(s.Close)
	failed, err := s.Add(t.Context(), tui.WithPrefixf("fetch"))
	assert.NoError(t, err)
	kept, err := s.Add(t.Context(), tui.WithPrefixf("build"), tui.WithKeep())
	assert.NoError(t, err)
	failed.Fail(errors.New("timeout"))
	kept.Update("done")
	assert.NoError(t, kept.Close())
	// updates after completion do not change what was reported
	failed.Update("retrying")
	kept.Update("restarted")
	// spinners apply changes in order, so a frame with this one has all of them
	_, err = s.Add(context.Background(), tui.WithPrefixf("sentinel"))
	assert.NoError(t, err)
	term.waitRows(t, "✖ fetch: timeout", "■ build: done", "- sentinel:")
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
	mu     sync.Mutex
	rest   []byte // the part of a key that did not fit into the last read
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

// Read returns one key per call, as a terminal in raw mode does. A key
// longer than p, such as a terminal reply, is returned over several reads.
func (k *keyboard) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	k.mu.Lock()
	if len(k.rest) > 0 {
		n := copy(p, k.rest)
		k.rest = k.rest[n:]
		k.mu.Unlock()
		return n, nil
	}
	k.mu.Unlock()
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
		n := copy(p, key)
		k.mu.Lock()
		k.rest = []byte(key[n:])
		k.mu.Unlock()
		return n, nil
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
	raw      []byte // everything written, for checks of the bytes
}

func newScreen(width int) *screen {
	return &screen{width: width, changed: make(chan struct{})}
}

// Write applies one write of the arbiter, which is always a complete redraw.
func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw = append(s.raw, p...)
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
	if s.esc[1] == ']' { // an OSC request, ended by BEL or ST, draws nothing
		if b == 0x07 || b == '\\' && s.esc[len(s.esc)-2] == 0x1b {
			s.esc = nil
		}
		return
	}
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
		// a later terminal of the same test may have restored os.Stderr already
		closer, ok := tui.Stderr().(io.Closer)
		if ok && closer != io.Closer(os.Stderr) {
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

// newASCIITerminal is newTerminal with the ASCII theme: the active row is
// marked with ">" and the answer line starts with "+".
func newASCIITerminal(t *testing.T) *terminal {
	t.Helper()
	asciiTheme(t)
	return newTerminal(t, 12)
}

// asciiTheme installs the ASCII preset as the default theme for the test.
func asciiTheme(t *testing.T) {
	t.Helper()
	th := tui.NewTheme()
	th.Symbols = tui.ASCIISymbols()
	tui.UseTheme(t, th)
}

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
	th := tui.NewTheme()
	th.Templates.Multichoice.Answer = `ANSWER {{ .Count }}{{ range .Values }} {{ . }}{{ else }} none{{ end }}`
	tui.UseTheme(t, th)
	var out bytes.Buffer
	got, err := tui.Multichoice(t.Context(), "Services", items,
		tui.WithInput(strings.NewReader("\r")), tui.WithOutput(&out),
		tui.WithOptions(tui.WithOneReturn(), tui.WithTemplate(".Name", ".Region"), tui.WithDefault("worker")))
	if err != nil || !reflect.DeepEqual(got, items[1:]) {
		t.Fatalf("got %v, %v", got, err)
	}
	if strings.Count(out.String(), "ANSWER 1 worker") != 1 || !strings.Contains(out.String(), "◉ worker") {
		t.Fatal(out.String())
	}
	got[0].Name = "changed"
	if items[1].Name != "worker" {
		t.Fatal("result aliases source slice")
	}
	t.Run("generated collection answer", func(t *testing.T) {
		tui.UseTheme(t, tui.NewTheme())
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
			tui.WithTemplate(`.Name ", " .Region`),
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
	th := tui.NewTheme()
	th.Templates.Multichoice.Answer = `ANSWER {{ .Count }}{{ range .Values }} {{ . }}{{ end }}`
	tui.UseTheme(t, th)
	var out bytes.Buffer
	keys, err := tui.MultichoiceKVK(
		t.Context(),
		"Keys",
		values,
		tui.WithDefault("b"),
		tui.WithInput(strings.NewReader("\x1b[B \r")),
		tui.WithOutput(&out),
	)
	if err != nil || !reflect.DeepEqual(keys, []string{"alpha", "beta"}) {
		t.Fatalf("%v, %v", keys, err)
	}
	if strings.Count(out.String(), "ANSWER 2 alpha beta") != 1 {
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
	)
	if err != nil || !reflect.DeepEqual(selected, [][]int{{1}, {1}}) {
		t.Fatalf("%v, %v", selected, err)
	}
	if strings.Count(out.String(), "ANSWER 2 alpha beta") != 1 {
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

func TestMultichoiceSharesTerminalWithProgressAndLogs(t *testing.T) {
	term := newASCIITerminal(t)
	progress, err := tui.NewMaxProgressBar(t.Context(), "Download", 4)
	if err != nil {
		t.Fatal(err)
	}
	progress.Add(1)
	term.waitRows(t, "Download  25%...")
	res := prompt(func() (string, error) {
		values, err := tui.Multichoice(t.Context(), "Pick", []string{"a", "b"})
		return fmt.Sprint(values), err
	})
	term.waitRows(t, "Download  25%...", "? Pick", "| > o a", "|   o b", "| up/down move...")
	if _, err := fmt.Fprintln(tui.Stderr(), "log line"); err != nil {
		t.Fatal(err)
	}
	term.waitRows(t, "log line", "Download  25%...", "? Pick", "| > o a", "|   o b", "| up/down move...")
	term.press(t, " ")
	term.press(t, keyDown)
	term.waitRows(t, "log line", "Download  25%...", "? Pick", "|   x a", "| > o b", "| up/down move...")
	term.press(t, keyEnter)
	if got := answer(t, res); got != "[a]" {
		t.Fatal(got)
	}
	term.waitRows(t, "log line", "+ Pick · a", "Download  25%...")
	progress.Add(3)
	if err := progress.Close(); err != nil {
		t.Fatal(err)
	}
	term.waitRows(t, "log line", "+ Pick · a")
	res = prompt(func() (string, error) { return tui.Input(t.Context(), "Next", tui.WithHide()) })
	term.press(t, "x")
	term.press(t, keyEnter)
	if got := answer(t, res); got != "x" {
		t.Fatal(got)
	}
}

func TestMultichoiceLazyManagedFilterAndConfirmation(t *testing.T) {
	term := newASCIITerminal(t)
	items := make(chan string)
	res := prompt(func() (string, error) {
		values, err := tui.MultichoiceLazy(t.Context(), "Pick", feed(items), tui.WithDefault("api"))
		return fmt.Sprint(values), err
	})
	term.waitRows(t, "? Pick", "| up/down move...")
	items <- "api"
	term.waitRows(t, "? Pick", "| > o api", "| up/down move...")
	term.press(t, "z")
	term.waitRows(t, "? Pick", "| No matches", "| up/down move...")
	items <- "zzz"
	term.waitRows(t, "? Pick", "| > o zzz", "| up/down move...")
	term.press(t, " ")
	term.press(t, keyEnter)
	term.waitRows(t, "? Pick", "| > x zzz", "| up/down move...")
	read := term.blockedRead(t)
	close(items)
	read <- keyBackspace
	term.waitRows(t, "? Pick", "|   x api", "| > x zzz", "| up/down move...")
	term.press(t, keyEnter)
	if got := answer(t, res); got != "[api zzz]" {
		t.Fatal(got)
	}
	term.waitRows(t, "+ Pick · api, zzz")
}

func TestMultichoiceManagedResizePreservesIdentity(t *testing.T) {
	term := newASCIITerminal(t)
	res := prompt(func() (string, error) {
		values, err := tui.Multichoice(t.Context(), "Pick", []string{"a", "b", "c", "d", "e"})
		return fmt.Sprint(values), err
	})
	term.waitRows(t, "? Pick", "| > o a", "|   o b", "|   o c", "|   o d", "|   o e", "| up/down move...")
	term.press(t, keyDown)
	term.press(t, keyDown)
	term.press(t, " ")
	read := term.blockedRead(t)
	// two rows and the count, with the row below the frame free
	term.resize(t, 40, 5)
	term.waitRows(t, "? Pick", "|   o b", "| > x c", "| . 2 more")
	term.resize(t, 40, 12)
	term.waitRows(t, "? Pick", "|   o a", "|   o b", "| > x c", "|   o d", "|   o e", "| up/down move...")
	read <- keyEnter
	if got := answer(t, res); got != "[c]" {
		t.Fatal(got)
	}
}

func TestMultichoiceManagedCancelAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(strconv.FormatBool(deadline), func(t *testing.T) {
			term := newASCIITerminal(t)
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 750*time.Millisecond)
			}
			defer cancel()
			res := prompt(func() (string, error) {
				values, err := tui.Multichoice(ctx, "Pick", []string{"a"})
				return fmt.Sprint(values), err
			})
			term.waitRows(t, "? Pick", "| > o a", "| up/down move...")
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
	// only the default can be checked: the preset's checked glyph means api
	if !s.sent && strings.Contains(string(p), "◉") {
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
			asciiTheme(t)
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
				got, err := tui.Multichoice(ctx, "Pick", []string{"a", "b", "c", "d", "e"})
				selected <- got
				result <- err
			}()
			screen.waitRows(t, "? Pick", "| > o a", "|   o b", "|   o c", "|   o d", "|   o e", "| up/down move...")
			if _, err := master.WriteString("\x1b[B\x1b[B "); err != nil {
				t.Fatal(err)
			}
			screen.waitRows(t, "? Pick", "|   o a", "|   o b", "| > x c", "|   o d", "|   o e", "| up/down move...")
			// room for two items and the count, with the row below the frame free
			resizeTTY(t, tty, 40, 5)
			screen.waitRows(t, "? Pick", "|   o b", "| > x c", "| . 2 more")
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

// Phase 22: migrated widgets rendering together through the theme, on the
// shared terminal arbiter that a CLI uses.

// received returns every byte the terminal has received.
func (s *screen) received() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.raw)
}

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
	progress      string   // the start of the progress row
	input         []string // Name while typing x
	dropdown      []string // Pick with b focused
	checklist     []string // Tags with x checked
	answers       []string // retained rows of the answered prompts
}

var themedLayouts = []themedLayout{{
	spinner:   "~ fetch: pages",
	progress:  "Download  25% █...",
	input:     []string{"◆ Name", "│ x"},
	dropdown:  []string{"◆ Pick", "│   a", "│ ❯ b", "│ up/down move · type filter"},
	checklist: []string{"◆ Tags", "│ ❯ ◉ x", "│   ○ y", "│ up/down move · space toggle · type f…"},
	answers:   []string{"✔ Name · x", "✔ Pick · b", "✔ Tags · x", "✔ Go · Yes", "✔ Count · 3"},
}, {
	ascii:     true,
	spinner:   "~ fetch: pages",
	progress:  "Download  25% =...",
	input:     []string{"? Name", "| x"},
	dropdown:  []string{"? Pick", "|   a", "| > b", "| up/down move · type filter"},
	checklist: []string{"? Tags", "| > x x", "|   o y", "| up/down move · space toggle · type f…"},
	answers:   []string{"+ Name · x", "+ Pick · b", "+ Tags · x", "+ Go · Yes", "+ Count · 3"},
}, {
	inline:    true,
	spinner:   "~ fetch: pages",
	progress:  "Download  25% █...",
	input:     []string{"◆ Name x"},
	dropdown:  []string{"◆ Pick   a", "❯ b", "up/down move · type filter"},
	checklist: []string{"◆ Tags", "❯ ◉ x", "○ y", "up/down move · space toggle · type fil…"},
	answers:   []string{"✔ Name · x", "✔ Pick · b", "✔ Tags · x", "✔ Go · Yes", "✔ Count · 3"},
}, {
	inline:    true,
	ascii:     true,
	spinner:   "~ fetch: pages",
	progress:  "Download  25% =...",
	input:     []string{"? Name x"},
	dropdown:  []string{"? Pick   a", "> b", "up/down move · type filter"},
	checklist: []string{"? Tags", "> x x", "o y", "up/down move · space toggle · type fil…"},
	answers:   []string{"+ Name · x", "+ Pick · b", "+ Tags · x", "+ Go · Yes", "+ Count · 3"},
}}

func (l themedLayout) theme() tui.Theme {
	th := tui.NewTheme()
	th.Inline = l.inline
	if l.ascii {
		th.Symbols = tui.ASCIISymbols()
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
	term.waitRows(t, l.spinner, l.progress)
	_, err = fmt.Fprintln(tui.Stderr(), "log line")
	assert.NoError(t, err)
	live := []string{"log line", l.spinner, l.progress}
	term.waitRows(t, live...)

	var retained []string
	rows := func(frame ...string) []string {
		return append(append(append([]string{"log line"}, retained...), l.spinner, l.progress), frame...)
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

// waitQueries waits until the terminal has received n background queries.
// A probe may block on the keyboard before it writes its query.
func (term *terminal) waitQueries(t *testing.T, n int) {
	t.Helper()
	timeout := time.After(failsafe)
	for {
		term.screen.mu.Lock()
		got, changed := strings.Count(string(term.raw), backgroundQuery), term.changed
		term.screen.mu.Unlock()
		if got == n {
			return
		}
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("timed out waiting for %d background queries, got %d", n, got)
		}
	}
}

// answerProbe replies to the nth background query, sent by a prompt.
func (term *terminal) answerProbe(t *testing.T, n int, reply string) {
	t.Helper()
	read := term.blockedRead(t)
	term.waitQueries(t, n)
	read <- reply
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
	live := []string{"~ work", "Download  25% █..."}
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
	term.waitRows(t, "✔ Name · x", "~ work", "Download  25% █...", "~ broken")
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
	picking := []string{"? Pick", "| > a", "|   b", "| up/down move · type filter"}
	term.waitRows(t, append([]string{"~ first:", "Download  25% =..."}, picking...)...)

	next := themedLayouts[0].theme() // Guided Unicode
	next.Symbols.Spinner = []string{"*"}
	assert.NoError(t, tui.SetDefaultTheme(next))

	// redraws of running widgets, and a spinner added to a running group,
	// keep the theme they started with
	_, err = group.Add(t.Context(), tui.WithPrefixf("second"))
	assert.NoError(t, err)
	bar.Add(1)
	term.press(t, keyDown)
	running := []string{"~ first:", "~ second:", "Download  50% =..."}
	term.waitRows(t, append(running, "? Pick", "|   a", "| > b", "| up/down move · type filter")...)
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
	themes := map[string]tui.Theme{}
	for name, gap := range map[string]int{"A": 2, "B": 5} {
		th := l0.theme()
		th.Templates.Prompt.Label = tui.Template(name + " {{ .Label }}")
		th.Templates.Prompt.Answer = tui.Template(name + " {{ .Label }} · {{ .Value }}")
		th.Templates.Table.Header = tui.Template(name + "{{ .Header }}")
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
