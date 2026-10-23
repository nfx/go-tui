// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst_test

import (
	"context"
	"strings"
	"testing"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/terst"
)

// TestMain treats the in-memory terminals of the tests as real ones, and
// keeps prompts interactive when the tests run in a CI job.
func TestMain(m *testing.M) { terst.Main(m) }

// greet is the code under test: it asks for a name and greets.
func greet(ctx context.Context) (string, error) {
	name, err := tui.Input(ctx, "Name", tui.WithDefault("stranger"))
	if err != nil {
		return "", err
	}
	return "Hello, " + name + "!", nil
}

// pickColor is the code under test: it asks to choose a color.
func pickColor(ctx context.Context) (string, error) {
	return tui.Dropdown(ctx, "Color", []string{"red", "green", "blue"})
}

// result is what the code under test returned.
type result struct {
	value string
	err   error
}

// run calls fn in the background, since a prompt blocks until it is
// answered, and returns a channel for its result.
func run(fn func() (string, error)) <-chan result {
	res := make(chan result, 1)
	go func() {
		value, err := fn()
		res <- result{value, err}
	}()
	return res
}

func TestTypingAnswer(t *testing.T) {
	terst.UseASCII(t)
	keys, screen := terst.UseTerminal(t)
	res := run(func() (string, error) { return greet(t.Context()) })

	// the default is editable text: erase it before typing over it
	screen.AwaitVisible("? Name", "| stranger")
	keys.Type(strings.Repeat(terst.KeyBackspace, len("stranger")) + "Ada" + terst.KeyEnter)

	got := terst.Recv(t, res)
	if got.err != nil || got.value != "Hello, Ada!" {
		t.Fatalf("got %q, %v", got.value, got.err)
	}
	screen.AwaitVisible("+ Name · Ada")
}

func TestAcceptingDefault(t *testing.T) {
	terst.UseASCII(t)
	keys, screen := terst.UseTerminal(t)
	res := run(func() (string, error) { return greet(t.Context()) })

	screen.AwaitVisible("? Name", "| stranger")
	keys.Send(terst.KeyEnter)

	got := terst.Recv(t, res)
	if got.err != nil || got.value != "Hello, stranger!" {
		t.Fatalf("got %q, %v", got.value, got.err)
	}
}

func TestPickingItem(t *testing.T) {
	terst.UseASCII(t)
	keys, screen := terst.UseTerminal(t)
	res := run(func() (string, error) { return pickColor(t.Context()) })

	screen.AwaitVisible("? Color", "| > red", "|   green", "|   blue", "| ^v move...")
	keys.Send(terst.KeyDown)
	screen.AwaitVisible("? Color", "|   red", "| > green", "|   blue", "| ^v move...")
	keys.Send(terst.KeyEnter)

	got := terst.Recv(t, res)
	if got.err != nil || got.value != "green" {
		t.Fatalf("got %q, %v", got.value, got.err)
	}
}

func TestDefaultWithoutTerminal(t *testing.T) {
	terst.Quiet(t)

	got, err := greet(t.Context())
	if err != nil || got != "Hello, stranger!" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTableColorFollowsEnvironment(t *testing.T) {
	type row struct{ Name, Color string }
	for env, want := range map[string]string{
		"TERM=dumb":     "NAME  COLOR  \nAda   green  \n",
		"FORCE_COLOR=3": "\x1b[1mNAME\x1b[0m  \x1b[1mCOLOR\x1b[0m  \nAda   green  \n",
	} {
		t.Run(env, func(t *testing.T) {
			terst.WithEnv(t, env)
			var out strings.Builder

			err := tui.TableAuto(&out, []row{{"Ada", "green"}})
			if err != nil || out.String() != want {
				t.Fatalf("got %q, %v", out.String(), err)
			}
		})
	}
}
