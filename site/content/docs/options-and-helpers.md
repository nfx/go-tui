---
title: "Shared Options and Helpers"
weight: 20
---

`go-tui` uses `opt` functions. Every option supports a fixed set of widgets,
and widgets reject options they do not support with `ErrWrongWidget`, so a
misplaced option fails instead of silently doing nothing.

| Option | Supported by |
|---|---|
| `WithContext`, `WithTimeout`, `WithInput`, `WithOutput` | `Input`, `Password`, dropdowns, `Confirm`, `FilePicker`, `NewSpinners`, progress bars |
| `WithDefault`, `WithHide`, `WithLabelTemplate`, `WithAnswerTemplate` | `Input`, `Password`, dropdowns, `Confirm`, `FilePicker` |
| `WithNonEmpty` | `Input`, `Password` |
| other `With*Template`, `WithTemplate`, `WithOneReturn` | dropdowns, `Confirm`, `FilePicker` |
| `WithStartDir`, `WithExtensions`, `WithIgnoreUp`, `WithIgnoreDirs`, `WithShowHidden` | `FilePicker` |
| `WithPrefixf`, `WithKeep`, `WithFrames` | `Spinners.Add` |
| `WithFormatRate`, `WithWorkers` | progress bars |
| `WithColumn*`, `WithIncludeColumns`, `WithSkipColumns`, `WithMaxWidth`, `WithMultilineCells`, `WithFloat64*` | `Table`, `TableAuto`, `TableIter`, `Facts` |

Tables take their writer as an argument, so they accept none of the shared
options below. Column options only shape auto-generated templates and have no
effect when you pass an explicit row template.

## WithContext

Sets widget context directly.

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

value, err := tui.Input("Name", tui.WithContext(ctx))
```

Use this when widget lifetime should follow your context tree.

## WithTimeout

Wraps current widget context with `context.WithTimeout`.

```go
value, err := tui.Input("Name", tui.WithTimeout(15*time.Second))
```

Use this to enforce an upper bound on interaction time.

## WithInput

Overrides input source for a widget.

```go
in := strings.NewReader("alice\n")
value, err := tui.Input("Name", tui.WithInput(in))
```

Useful for tests and scripted flows.

## WithOutput

Overrides output destination.

```go
var out bytes.Buffer
_, err := tui.Input("Name", tui.WithOutput(&out))
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

## WithOptions

Composes multiple options into one that can be reused across widgets. Each
member applies where the widget supports it and is skipped elsewhere. The bundle
fails with `ErrWrongWidget` only when the widget supports none of its members.
Invalid values, such as `WithWorkers(0)`, are always reported.

```go
common := tui.WithOptions(
	tui.WithTimeout(20*time.Second),
	tui.WithOutput(os.Stderr),
)

_, err := tui.Input("Name", common)
```

## WithFn

Registers a global template helper function.

```go
tui.WithFn("upper", strings.ToUpper)
```

Then use it in templates: `{{ upper .Name }}`.

All predefined helper functions are documented on
[Template Functions](template-functions.md).

## Common errors

- `ErrNoItems`: selection widget called with empty items.
- `ErrInvalidState`: invalid option value or configuration.
- `ErrUnsupportedPlatform`: platform-specific feature unavailable.
- `ErrWrongWidget`: option is not supported by the widget it was passed to.
