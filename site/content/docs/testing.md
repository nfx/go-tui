---
title: "Testing"
weight: 110
---

Color depth, symbols and whether prompts are interactive all come from the
environment. The first part of this page previews other terminals by hand, and
[Tests with terst](#tests-with-terst) checks your prompts in `go test`. Setting a few variables lets you preview, from your own terminal,
how a program looks and behaves for someone else. Every recipe below works
with any program, for example:

```sh
TERM=linux COLORTERM= go -C examples run ./theme
```

These variables are read once, when the program starts.

There are examples in the source repository to try these recipes on. They are
a Go module of their own, in the `examples` directory, so the commands on this
page run them from the repository root with `go -C examples`.

## Color depth

| Looks like | Variables |
| --- | --- |
| iTerm2, Ghostty, kitty, WezTerm, Windows Terminal: 24-bit color | `COLORTERM=truecolor` |
| macOS Terminal: 256 colors | `TERM=xterm-256color COLORTERM=` |
| A basic xterm: 16 colors | `TERM=xterm COLORTERM=` |
| A dumb terminal: no color, no attributes | `TERM=dumb COLORTERM=` |
| A user who turned color off: bold and other attributes only | `NO_COLOR=1` |
| Any depth, chosen directly | `FORCE_COLOR=1` (16), `2` (256), `3` (24-bit), `0` (none) |

Clear `COLORTERM` when you aim for fewer colors, since your own terminal
likely sets it. `FORCE_COLOR` wins over every other variable, `NO_COLOR` and
`CI` included, and also colors output to pipes and files. `CLICOLOR_FORCE=1`
colors pipes and files too, at the depth `TERM` and `COLORTERM` suggest. With
16 colors, your terminal draws them from its own palette, not the palette of
the terminal you preview.

## Symbols

| Looks like | Variables |
| --- | --- |
| A UTF-8 terminal: Unicode symbols | `LC_ALL=en_US.UTF-8` |
| The Linux virtual console: 16 colors, ASCII symbols | `TERM=linux COLORTERM=` |
| A non-UTF-8 locale: ASCII symbols | `LC_ALL=en_US.ISO-8859-1` |
| The legacy Windows console: 16 colors, ASCII symbols | `TERM=xterm COLORTERM= LC_ALL=en_US.CP437` |

The locale comes from the first of `LC_ALL`, `LC_CTYPE` and `LANG` that is
set, so `LC_ALL` overrides whatever your shell exports. See
[Symbols](../themes/#symbols) for the full rule, including Windows.

## Interactivity

| Looks like | Command |
| --- | --- |
| A CI log: Unicode symbols, no color, prompts answer their defaults | `CI=true go -C examples run ./number` |
| A CI log that opts into color | `CI=true FORCE_COLOR=3 go -C examples run ./number` |
| A script that pipes the prompts away | `go -C examples run ./number 2>&1 \| cat` |
| A script that discards the prompts | `go -C examples run ./number 2>/dev/null` |

Without a terminal, a prompt with `WithDefault` prints nothing and returns
its default, and one without it fails with `no tty`. Spinners and progress
bars draw nothing and log their updates through `slog.Default()` instead. See
[Defaults without a terminal](../options-and-helpers/#defaults-without-a-terminal)
and [Progress bars without a terminal](../progressbar/#without-a-terminal).

`CI` counts as set for any value but an empty one, `0` and `false`, so
`CI=0` previews an ordinary terminal even inside a CI job. Prompts draw on
stderr, so it is stderr that must leave the terminal: `| cat` alone pipes
only stdout and keeps the prompts interactive.

## Background

| Looks like | Variables |
| --- | --- |
| A light background | `COLORFGBG=0;15` |
| A dark background | `COLORFGBG=15;0` |

Quote the value in a shell, as in `COLORFGBG='0;15'`, since `;` ends a
command. `COLORFGBG` applies to spinners, progress bars and tables. A prompt
asks the terminal for its background, so your own terminal decides how a
prompt looks. Set `theme.Light = true` to preview prompts on a light
background.

## Combining them

The variables are independent, so one preview can combine several:

```sh
# a light 256-color terminal without Unicode fonts
TERM=xterm-256color COLORTERM= LC_ALL=en_US.ISO-8859-1 COLORFGBG='0;15' go -C examples run ./table

# a CI log with color, as GitHub Actions shows it with FORCE_COLOR set
CI=true FORCE_COLOR=3 go -C examples run ./progress-bar
```

## Tests with terst

The `terst` package runs your prompts in `go test` against an emulated
terminal: the test types keys and waits for what the screen shows. Its
examples are in
[`terst/examples_test.go`](https://github.com/nfx/go-tui/blob/main/terst/examples_test.go).
Start with a `TestMain` in each package that tests prompts:

```go
import (
	"testing"

	tui "github.com/nfx/go-tui"
	"github.com/nfx/go-tui/terst"
)

func TestMain(m *testing.M) { terst.Main(m) }
```

`terst.Main` treats the in-memory terminals of the tests as real ones, and
keeps prompts interactive when the tests run in a CI job.

### Typing answers

`terst.UseTerminal` makes an 80×24 terminal the input and output of every
widget for the test. It returns the keyboard and the screen. A prompt blocks
until it is answered, so call your code in a goroutine:

```go
// greet is the code under test: it asks for a name and greets.
func greet(ctx context.Context) (string, error) {
	name, err := tui.Input(ctx, "Name", tui.WithDefault("stranger"))
	if err != nil {
		return "", err
	}
	return "Hello, " + name + "!", nil
}

func TestTypingAnswer(t *testing.T) {
	terst.UseASCII(t)
	keys, screen := terst.UseTerminal(t)
	res := make(chan string, 1)
	go func() {
		greeting, _ := greet(t.Context())
		res <- greeting
	}()

	// the default is editable text: erase it before typing over it
	screen.AwaitVisible("? Name", "| stranger")
	keys.Type(strings.Repeat(terst.KeyBackspace, len("stranger")) + "Ada" + terst.KeyEnter)

	if got := terst.Recv(t, res); got != "Hello, Ada!" {
		t.Fatalf("got %q", got)
	}
	screen.AwaitVisible("+ Name · Ada")
}
```

`AwaitVisible` waits until the screen shows exactly the given rows, blank
rows left out and each row trimmed. A row that ends with `...` only has to
start with what precedes it, which keeps key hints out of the test:

```go
screen.AwaitVisible("? Color", "| > red", "|   green", "|   blue", "| ^v move...")
keys.Send(terst.KeyDown)
screen.AwaitVisible("? Color", "|   red", "| > green", "|   blue", "| ^v move...")
keys.Send(terst.KeyEnter)
```

`UseASCII` installs the default theme with ASCII symbols, so the rows in the
test read the same in every editor. `UseTheme` installs your own theme.

| Helper | Does |
| --- | --- |
| `keys.Type(s)` | Sends `s` one key at a time, as a user types it |
| `keys.Send(chunks...)` | Sends each chunk as one read: a chunk of several keys arrives as a paste |
| `terst.KeyEnter`, `KeyEsc`, `KeyTab`, `KeyBackspace`, `KeyUp`, `KeyDown`, `KeyLeft`, `KeyRight` | The sequences a terminal sends for these keys |
| `screen.AwaitVisible(rows...)` | Waits until the screen shows the rows |
| `screen.Visible()` | The rows the screen shows now, one per line |
| `screen.AwaitOutput(parts...)` | Waits for a write that contains every part, escape sequences included |
| `terst.Recv(t, ch)` | Receives from `ch` |

Every wait fails the test after two seconds. A failed `AwaitVisible` reports
the rows the screen showed, which is also how to find out the rows to wait
for.

### Without a terminal

`terst.Quiet` runs the test as a script without a terminal would run your
program. Prompts with `WithDefault` return their default, and the test fails
when a widget reads input or writes anything:

```go
func TestDefaultWithoutTerminal(t *testing.T) {
	terst.Quiet(t)

	got, err := greet(t.Context())
	if err != nil || got != "Hello, stranger!" {
		t.Fatalf("got %q, %v", got, err)
	}
}
```

### Environment

`terst.WithEnv` gives the test the environment of another terminal, as if
the variables it lists were the only ones set. It takes the variables of the
recipes above:

```go
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
```

`terst.StripSGR` removes colors and attributes from output, for tests that
check text only.

Every `terst` helper restores what it changed when the test ends. The helpers
replace process-wide defaults, so tests that use them can't call
`t.Parallel`.
