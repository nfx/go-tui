---
title: "Shared Options and Helpers"
weight: 20
---

`go-tui` uses `opt` functions. Every option supports a fixed set of widgets,
and widgets reject options they do not support with `ErrWrongWidget`, so a
misplaced option fails instead of silently doing nothing.

| Option | Supported by |
|---|---|
| `WithInput`, `WithOutput` | `Input`, `Password`, dropdowns, multichoice, `Confirm`, `FilePicker`, `NewSpinners`, progress bars |
| `WithDefault`, `WithHideAnswer` | `Input`, `Password`, `Number`, dropdowns, multichoice, `Confirm`, `FilePicker` |
| `WithNonEmpty`, `WithRegex`, `WithValidation` | `Input`, `Password` |
| `WithTemplate` | dropdowns, multichoice, `Confirm`, `FilePicker` |
| `WithOneReturn` | dropdowns, `Confirm`, `FilePicker` |
| `WithStartDir`, `WithExtensions`, `WithIgnoreUp`, `WithIgnoreDirs`, `WithShowHidden` | `FilePicker` |
| `WithPrefixf`, `WithKeep` | `Spinners.Add` |
| `WithFormatRate`, `WithWorkers` | progress bars |
| `WithColumn*`, `WithIncludeColumns`, `WithSkipColumns`, `WithMaxWidth`, `WithMultilineCells`, `WithFloat64*` | `Table`, `TableAuto`, `TableIter`, `Facts` |

Tables take their writer as an argument, so they accept none of the shared
options below. Column options only shape auto-generated templates and have no
effect when you pass an explicit row template.

## Defaults without a terminal

`WithDefault(d any)` is also the batch answer. Every prompt follows one rule:
**when the output is not a terminal it returns its explicit default, otherwise
an error wrapping `ErrNoTTY`**. A pipe, a redirected file or a `bytes.Buffer`
is not a terminal, however the input is wrapped. Neither is any output when
`CI` is set to anything but `0` or `false`, since nobody answers prompts or
watches redraws in a CI job, even on a pseudo-terminal. Spinners and progress
bars likewise draw nothing there. The answer reads no input and
prints no prompt, answer or escape codes. An invalid, missing or ambiguous
default is `ErrInvalidOption`, with the validation error still reachable through
`errors.Is`. There is no extra option to enable this. See
[Input](../input/), [Number](../number/), [dropdowns](../dropdowns/),
[multichoice](../multichoice/) and [FilePicker](../filepicker/); `Confirm`
returns `false` where the others return an error.

To verify it, redirect the prompt's output away from the terminal, for
example `go run ./examples/prompt/number/main.go 2>/dev/null` (prompts draw on stderr),
or pipe stderr, not stdout, through `cat` with `2>&1 | cat`:
a prompt with `WithDefault` prints nothing and returns the
default, and one without it fails with `no tty`.

## Context

Interactive and long-running widgets (`Input`, `Password`, dropdowns, multichoice, `Confirm`,
`FilePicker`, `NewSpinners` and progress bars) take a `context.Context` as the
first argument. Cancelling it, for example on Ctrl-C or a parent shutdown,
stops the widget and its workers. For an upper bound on interaction time,
derive the context with `context.WithTimeout` and cancel it when done.

```go
ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
defer cancel()

value, err := tui.Input(ctx, "Name")
```

## Input validation and feedback

`WithNonEmpty`, `WithRegex`, and `WithValidation` check confirmed input in option
order and display the first failure below the editable answer.
The `Feedback` slot of the theme customizes that row. These options support both `Input`
and `Password`; see [Input and Password](input.md#withvalidation) for examples and
the validator context contract.

## WithInput

Overrides input source for a widget.

```go
in := strings.NewReader("alice\n")
value, err := tui.Input(ctx, "Name", tui.WithInput(in))
```

Useful for tests and scripted flows. A reader that is not an `*os.File` is
treated as scripted input: the prompt skips raw mode and the managed viewport,
assumes an 80x24 terminal, and writes plain frames, including ANSI cursor
sequences, to the output. Send keys as terminal bytes, for example `\r` for
Enter and `\x1b[B` for Down. This applies to `Input`, `Password`, dropdowns,
multichoice, `Confirm` and `FilePicker`. Spinners and progress bars still need a terminal and
stay silent without one.

## WithOutput

Overrides output destination.

```go
var out bytes.Buffer
_, err := tui.Input(ctx, "Name", tui.WithOutput(&out))
```

Useful for tests and custom terminal routing.

## Stderr

Returns `go-tui`'s package-default append-only terminal writer.

```go
slog.SetDefault(slog.New(
	tint.NewHandler(tui.Stderr(), nil),
))
```

Use this when logs or other external terminal output should coexist with default
`go-tui` widgets on the same TTY.

- On a TTY, `Stderr()` routes writes through the shared terminal arbiter so log
  lines and widgets are redrawn in a stable order.
- On a non-TTY, `Stderr()` returns the configured raw writer directly, so daemon
  and batch logging stay simple.
- Only cooperating writers routed through `tui.Stderr()` participate in that
  coordination. Direct writes to raw `os.Stderr` bypass it.

## WithFn

Registers a global template helper function for operations started afterwards.

```go
tui.WithFn("upper", strings.ToUpper)
```

Then use it in templates: `{{ upper .Name }}`. It panics on an invalid name or
signature, or on a built-in helper's name. `Theme.Funcs` with
`SetDefaultTheme` returns an error instead; see [Themes](themes.md).

All predefined helper functions are documented on
[Template Functions](template-functions.md).

## Common errors

- `ErrNoItems`: selection widget called with empty items.
- `ErrInvalidOption`: nil multichoice lazy sequence, nil input validator, invalid regex pattern, or blank regex feedback message.
- `ErrNoSpace`: insufficient terminal space, including fewer than two rows when input rejection feedback is needed.
- `ErrEmptyLazyResult`: a completed lazy selection source contains no items.
- `ErrInvalidState`: invalid option value or configuration.
- `ErrUnsupportedPlatform`: platform-specific feature unavailable.
- `ErrWrongWidget`: option is not supported by the widget it was passed to.
