---
title: "Shared Options and Helpers"
weight: 20
---

`go-tui` uses `opt` functions. Most widgets accept the shared options below.

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

Composes multiple options into a reusable option.

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
- `ErrInvalidState`: invalid option/configuration.
- `ErrUnsupportedPlatform`: platform-specific feature unavailable.
- `ErrWrongWidget`: option targets a different widget type.
