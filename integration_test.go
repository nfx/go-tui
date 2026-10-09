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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/assert"
)

func TestDropdownDefaultSelectsClosestMatch(t *testing.T) {
	term := newTerminal(t, 40, 12)
	res := prompt(func() (string, error) {
		return tui.Dropdown("Pick", []string{"apple pie", "banana", "apple"},
			plain, tui.WithDefault("apple"))
	})
	// two items share the prefix, so the closest one is ranked first and selected
	term.waitRows(t, "Pick * apple", "- apple pie", "- banana")
	term.press(t, keyEnter)
	assert.Equal(t, "apple", answer(t, res))
	term.waitRows(t, "Pick: apple")
}

func TestDropdownLazyDefaultSelectsBestRankedArrival(t *testing.T) {
	term := newTerminal(t, 40, 12)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy("Pick", feed(items), plain, tui.WithDefault("cat"))
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
	term := newTerminal(t, 40, 12)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy("Pick", feed(items), plain, tui.WithDefault("cat"))
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
	term := newTerminal(t, 40, 12)
	items := make(chan string)
	res := prompt(func() (string, error) {
		return tui.DropdownLazy("Pick", feed(items), plain)
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
	term := newTerminal(t, 40, 12)
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
		return tui.Dropdown("Pick", []string{"a", "b", "c", "d", "e"}, plain)
	})
}

func TestDropdownLazyResizeWhileReadingKeepsSelection(t *testing.T) {
	testResizeWhileReading(t, func() (string, error) {
		return tui.DropdownLazy("Pick", values("a", "b", "c", "d", "e"), plain)
	})
}

func TestWidgetsShareTerminal(t *testing.T) {
	term := newTerminal(t, 40, 12)
	p, err := tui.NewMaxProgressBar("Download", 4)
	assert.NoError(t, err)
	p.Add(1)
	term.waitRows(t, "Download 25%...")

	res := prompt(func() (string, error) {
		return tui.Dropdown("Pick", []string{"a", "b"}, plain)
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
		return tui.Input("Name")
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
	term := newTerminal(t, 40, 6)
	p, err := tui.NewMaxProgressBar("Copy", 4)
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
	term := newTerminal(t, 40, 6)
	s, err := tui.NewSpinners()
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
func newTerminal(t *testing.T, width, height int) *terminal {
	t.Helper()
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
