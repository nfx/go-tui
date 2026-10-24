---
title: "Shared Options"
weight: 120
---

`go-tui` uses `opt` functions. Every option supports a fixed set of widgets,
and widgets reject options they do not support with [`ErrWrongWidget`](#common-errors), so a
misplaced option fails instead of silently doing nothing.

| Option | Supported by |
|---|---|
| [`WithDefault`](#defaults-without-a-terminal), [`WithHideAnswer`](dropdowns.md#withhideanswer) | [`Input`](input.md), [`Password`](input.md), [`Number`](number.md), [dropdowns](dropdowns.md), [multichoice](multichoice.md), [`Confirm`](dropdowns.md#confirm), [`FilePicker`](filepicker.md#filepicker) |
| [`WithNonEmpty`](input.md#withnonempty), [`WithRegex`](input.md#withregex), [`WithValidation`](input.md#withvalidation) | [`Input`](input.md), [`Password`](input.md) |
| [`WithTemplate`](dropdowns.md#withtemplate) | [dropdowns](dropdowns.md), [multichoice](multichoice.md), [`Confirm`](dropdowns.md#confirm), [`FilePicker`](filepicker.md#filepicker) |
| [`WithOneReturn`](dropdowns.md#withonereturn) | [dropdowns](dropdowns.md), [`Confirm`](dropdowns.md#confirm), [`FilePicker`](filepicker.md#filepicker) |
| [`WithStartDir`](filepicker.md#withstartdir), [`WithExtensions`](filepicker.md#withextensions), [`WithIgnoreUp`](filepicker.md#withignoreup), [`WithIgnoreDirs`](filepicker.md#withignoredirs), [`WithShowHidden`](filepicker.md#withshowhidden) | [`FilePicker`](filepicker.md#filepicker) |
| [`WithPrefixf`](spinners.md#withprefixf), [`WithKeep`](spinners.md#withkeep) | [`Spinners.Add`](spinners.md) |
| [`WithFormatRate`](progressbar.md#withformatrate), [`WithWorkers`](progressbar.md#withworkers) | [progress bars](progressbar.md) |
| `WithColumn*`, `WithIncludeColumns`, `WithSkipColumns`, `WithMaxWidth`, `WithMultilineCells`, `WithFloat64*` | [`Table`](table.md#table), [`TableAuto`](table.md#tablex), [`TableIter`](table.md#tableiter), [`Facts`](table.md) |

[Tables](table.md) take their writer as an argument, so they accept none of the shared
options below. Column options only shape auto-generated templates and have no
effect when you pass an explicit row template.

## Defaults without a terminal

`WithDefault(d any)` is also the batch answer. Every prompt follows one rule:
**when the output is not a terminal it returns its explicit default, otherwise
an error wrapping [`ErrNoTTY`](#common-errors)**. A pipe, a redirected file or a `bytes.Buffer`
is not a terminal, however the input is wrapped. Neither is any output when
`CI` is set to anything but `0` or `false`, since nobody answers prompts or
watches redraws in a [CI](https://en.wikipedia.org/wiki/Continuous_integration) job, even on a pseudo-terminal. [Spinners](spinners.md) and [progress
bars](progressbar.md) likewise draw nothing there. The answer reads no input and
prints no prompt, answer or escape codes. An invalid, missing or ambiguous
default is [`ErrInvalidOption`](#common-errors), with the validation error still reachable through
`errors.Is`. There is no extra option to enable this. See
[Input](input.md), [Number](number.md), [dropdowns](dropdowns.md),
[multichoice](multichoice.md) and [FilePicker](filepicker.md#filepicker); [`Confirm`](dropdowns.md#confirm)
returns `false` where the others return an error.

To verify it, redirect the prompt's output away from the terminal, for
example `go run ./examples/prompt/number/main.go 2>/dev/null` (prompts draw on stderr),
or pipe stderr, not stdout, through `cat` with `2>&1 | cat`:
a prompt with `WithDefault` prints nothing and returns the
default, and one without it fails with `no tty`.

## Context

Interactive and long-running widgets ([`Input`](input.md), [`Password`](input.md), [dropdowns](dropdowns.md), [multichoice](multichoice.md), [`Confirm`](dropdowns.md#confirm),
[`FilePicker`](filepicker.md#filepicker), [`NewSpinners`](spinners.md) and [progress bars](progressbar.md)) take a `context.Context` as the
first argument. Cancelling it, for example on Ctrl-C or a parent shutdown,
stops the widget and its workers. For an upper bound on interaction time,
derive the context with `context.WithTimeout` and cancel it when done.

```go
ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
defer cancel()

value, err := tui.Input(ctx, "Name")
```

## Input validation and feedback

[`WithNonEmpty`](input.md#withnonempty), [`WithRegex`](input.md#withregex), and [`WithValidation`](input.md#withvalidation) check confirmed input in option
order and display the first failure below the editable answer.
The `Feedback` slot of the theme customizes that row. These options support both [`Input`](input.md)
and [`Password`](input.md); see [Input and Password](input.md#withvalidation) for examples and
the validator context contract.

## Stderr

Returns `go-tui`'s package-default append-only terminal writer.

```go
slog.SetDefault(slog.New(
	tint.NewHandler(tui.Stderr(), nil),
))
```

Use this when logs or other external terminal output should coexist with default
`go-tui` widgets on the same [TTY](https://en.wikipedia.org/wiki/Computer_terminal#Text_terminals).

- On a TTY, [`Stderr()`](#stderr) routes writes through the shared terminal arbiter so log
  lines and widgets are redrawn in a stable order.
- On a non-TTY, [`Stderr()`](#stderr) returns the configured raw writer directly, so daemon
  and batch logging stay simple.
- Only cooperating writers routed through [`tui.Stderr()`](#stderr) participate in that
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
[Template functions](themes.md#template-functions).

## Common errors

- `ErrNoTTY`: a prompt without an explicit `WithDefault` ran where the output is not a terminal.
- `ErrNoItems`: selection widget called with empty items.
- `ErrInvalidOption`: nil multichoice lazy sequence, nil input validator, invalid regex pattern, or blank regex feedback message.
- `ErrNoSpace`: insufficient terminal space, including fewer than two rows when input rejection feedback is needed.
- `ErrEmptyLazyResult`: a completed lazy selection source contains no items.
- `ErrInvalidState`: invalid option value or configuration.
- `ErrUnsupportedPlatform`: platform-specific feature unavailable.
- `ErrWrongWidget`: option is not supported by the widget it was passed to.
- `ErrSpinnerStopped`: a child was added to a spinner that has already closed or failed.
