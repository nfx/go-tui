---
title: "Input and Password"
weight: 40
---

APIs:

- `Input(label, opts...)`
- `Password(label, opts...)`

## Basic example

```go
value, err := tui.Input(ctx, "Project name")
password, err := tui.Password(ctx, "API token")
```

`Password` masks entered text and does not print an answer line.

## WithDefault

Sets initial text and cursor position.

```go
value, err := tui.Input(ctx, "Project name",
	tui.WithDefault("demo-service"),
)
```

## WithInput

Overrides input source.

```go
value, err := tui.Input(ctx, "Project name",
	tui.WithInput(in),
)
```

## WithOutput

Overrides output destination.

```go
value, err := tui.Input(ctx, "Project name",
	tui.WithOutput(out),
)
```

## WithOptions

Bundles common options.

```go
common := tui.WithOptions(
	tui.WithOutput(os.Stderr),
)

value, err := tui.Input(ctx, "Project name", common)
```

## Notes

- Arrow-left/right and backspace editing are supported.
- Paste buffers are handled as one input event.
- Timeout/cancel returns context error.
