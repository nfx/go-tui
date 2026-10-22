// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/theme"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
	"golang.org/x/term"
)

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
	th := theme.New()
	th.Templates.Multichoice.Answer = `ANSWER {{ .Count }}{{ range .Values }} {{ . }}{{ else }} none{{ end }}`
	terst.UseTheme(t, th)
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
		terst.UseTheme(t, theme.New())
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
	th := theme.New()
	th.Templates.Multichoice.Answer = `ANSWER {{ .Count }}{{ range .Values }} {{ . }}{{ end }}`
	terst.UseTheme(t, th)
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
	got, err = tui.Multichoice(t.Context(), "Bad", []string{"one"}, tui.WithTemplate("{{"))
	if got != nil || err == nil {
		t.Fatal(got, err)
	}
	got, err = tui.Multichoice(t.Context(), "Bad", []string{"one"}, tui.WithTemplate(".Missing"))
	if got != nil || err == nil {
		t.Fatal(got, err)
	}
	var out bytes.Buffer
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
	term.waitRows(t, "Download  25%  ...", "1 / 4 items...")
	res := prompt(func() (string, error) {
		values, err := tui.Multichoice(t.Context(), "Pick", []string{"a", "b"})
		return fmt.Sprint(values), err
	})
	term.waitRows(t, "Download  25%  ...", "1 / 4 items...", "? Pick", "| > o a", "|   o b", "| ^v move...")
	if _, err := fmt.Fprintln(tui.Stderr(), "log line"); err != nil {
		t.Fatal(err)
	}
	term.waitRows(t, "log line", "Download  25%  ...", "1 / 4 items...", "? Pick", "| > o a", "|   o b", "| ^v move...")
	term.press(t, " ")
	term.press(t, keyDown)
	term.waitRows(t, "log line", "Download  25%  ...", "1 / 4 items...", "? Pick", "|   x a", "| > o b", "| ^v move...")
	term.press(t, keyEnter)
	if got := answer(t, res); got != "[a]" {
		t.Fatal(got)
	}
	term.waitRows(t, "log line", "+ Pick · a", "Download  25%  ...", "1 / 4 items...")
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
	term.waitRows(t, "? Pick", "| ^v move...")
	items <- "api"
	term.waitRows(t, "? Pick", "| > o api", "| ^v move...")
	term.press(t, "z")
	term.waitRows(t, "? Pick", "| No matches", "| ^v move...")
	items <- "zzz"
	term.waitRows(t, "? Pick", "| > o zzz", "| ^v move...")
	term.press(t, " ")
	term.press(t, keyEnter)
	term.waitRows(t, "? Pick", "| > x zzz", "| ^v move...")
	read := term.blockedRead(t)
	close(items)
	read <- keyBackspace
	term.waitRows(t, "? Pick", "|   x api", "| > x zzz", "| ^v move...")
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
	term.waitRows(t, "? Pick", "| > o a", "|   o b", "|   o c", "|   o d", "|   o e", "| ^v move...")
	term.press(t, keyDown)
	term.press(t, keyDown)
	term.press(t, " ")
	read := term.blockedRead(t)
	// two rows and the count, with the row below the frame free
	term.resize(t, 40, 5)
	term.waitRows(t, "? Pick", "|   o b", "| > x c", "| 2 more · ^v move · space toggle · ty…")
	term.resize(t, 40, 12)
	term.waitRows(t, "? Pick", "|   o a", "|   o b", "| > x c", "|   o d", "|   o e", "| ^v move...")
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
			term.waitRows(t, "? Pick", "| > o a", "| ^v move...")
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
			terst.UseIO(t, tty, &ttyWriter{fd: tty.Fd(), screen: screen})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			selected := make(chan []string, 1)
			go func() {
				got, err := tui.Multichoice(ctx, "Pick", []string{"a", "b", "c", "d", "e"})
				selected <- got
				result <- err
			}()
			screen.waitRows(t, "? Pick", "| > o a", "|   o b", "|   o c", "|   o d", "|   o e", "| ^v move...")
			if _, err := master.WriteString("\x1b[B\x1b[B "); err != nil {
				t.Fatal(err)
			}
			screen.waitRows(t, "? Pick", "|   o a", "|   o b", "| > x c", "|   o d", "|   o e", "| ^v move...")
			// room for two items and the count, with the row below the frame free
			resizeTTY(t, tty, 40, 5)
			screen.waitRows(t, "? Pick", "|   o b", "| > x c", "| 2 more · ^v move · space toggle · ty…")
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
