---
title: "Progress Bars"
weight: 50
---

## NewMaxProgressBar

Creates a mutable progress bar with explicit max size.

```go
p, err := tui.NewMaxProgressBar(ctx, "Upload", 100)
if err != nil {
	return err
}
defer p.Close()

for range 100 {
	p.Add(1)
}
```

Use this when your total work count is known.

## NewSliceProgressBar

Wraps slice iteration and auto-advances progress as items are consumed.

```go
for item, err := range tui.NewSliceProgressBar(ctx, "Items", items) {
	if err != nil {
		return err
	}
	_ = item
}
```

Use this for sequential processing loops.

## NewParallelProgressBar

Processes slice items concurrently and updates one shared progress bar.

```go
err := tui.NewParallelProgressBar(ctx, "Jobs", jobs, func(job Job) error {
	return run(job)
}, tui.WithWorkers(8))
if err != nil {
	return err
}
```

Returns the first worker error and cancels remaining work.

## NewFileProgressReader

Wraps a reader and advances progress by bytes read.

```go
wrapped, err := tui.NewFileProgressReader(ctx, file, "Download")
if err != nil {
	return err
}
defer wrapped.Close()

_, err = io.Copy(dst, wrapped)
```

Requires size discovery through `Stat().Size()` or `Size() int64`.

Without a TTY, reads continue normally and progress is logged through
`slog.Default()` at INFO. Records include `label`, `percent`, `read` (bytes), and
`size` (bytes). A record is emitted when a read reaches a new 10% milestone.
Large reads log only the latest milestone reached; a single read of the whole
file logs 100% once. Repeated reads within the same milestone produce no logs.
An empty file logs 100% on EOF; an early EOF does not report unread bytes as
complete. Configure the default logger to choose the destination and format.

## WithFormatRate

Customizes per-second rate formatting.

```go
p, err := tui.NewMaxProgressBar(ctx, "Upload", 100,
	tui.WithFormatRate(func(v float64) string {
		return fmt.Sprintf("%.1f items", v)
	}),
)
```

## WithWorkers

Sets worker count for `NewParallelProgressBar`.

```go
err := tui.NewParallelProgressBar(ctx, "Jobs", jobs, runJob,
	tui.WithWorkers(8),
)
```

Value must be greater than zero.

## WithInput

Overrides input reader for widget internals.

```go
p, err := tui.NewMaxProgressBar(ctx, "Upload", 100,
	tui.WithInput(in),
)
```

## WithOutput

Overrides output destination.

```go
p, err := tui.NewMaxProgressBar(ctx, "Upload", 100,
	tui.WithOutput(out),
)
```

## WithOptions

Composes multiple progress options.

```go
opts := tui.WithOptions(
	tui.WithOutput(out),
)

p, err := tui.NewMaxProgressBar(ctx, "Upload", 100, opts)
```

## Appearance

By default, a bar has two rows: the label, the percentage and the bar, and a
hint row with the metrics below it.

```text
Download  62%  ━━━━━━━━━━━━━━━━━━━╸────────────
62.0 / 100.0 MiB · 6.2 MiB/s · 12s left
```

With `tui.ASCIISymbols()`:

```text
Download  62%  ===================>------------
62.0 / 100.0 MiB | 6.2 MiB/s | 12s left
```

The percentage always takes four columns, so the bar never moves as it
grows. A bar without a total shows `—` (`?` in ASCII) instead of a
percentage. The hint row shows the amount, the rate, the time left and, with
the elapsed time enabled, the elapsed time. When the row is too narrow, it
drops whole entries from the end; when even the first entry does not fit,
the hint row goes. A long label is shortened before the bar is. A terminal
too short for both rows gets the compact row. A bar on its own terminal
through `WithOutput` also keeps a row below them for the cursor, so it needs
three.

The hint row is the `Templates.Progress.Hint` slot. Like a prompt's key
hint, it receives a list of entries, each with `.Left` and `.Right`, such as
`6.2` and `MiB/s`. It does not inherit from `Templates.Prompt.Hint`.

```go
theme := tui.NewTheme()
theme.Templates.Progress.Hint = `{{ range . }}[{{ .Left }} {{ .Right }}] {{ end }}`
```

Set it to `tui.Hidden` to keep only the bar row.

### Compact layout

`theme.Progress.Compact = true` keeps everything on one row,
as earlier versions did, and drops metrics from the end to keep the bar:

```text
Download 62%  ━━━━━━━╸──── 6.2 MiB/s · 12s remaining
```

In the stacked layout, `Templates.Progress.Frame` receives an empty
`.Metrics`. A custom frame that shows `.Metrics` after the bar keeps
working once it selects the compact layout:

```go
theme := tui.NewTheme()
theme.Progress.Compact = true
theme.Templates.Progress.Frame = `{{ .Label }} [{{ .Bar }}] {{ .Metrics }}`
```

`Templates.Progress.Metrics` applies to the compact layout only, and
`Templates.Progress.Hint` to the stacked layout only.

## Behavior notes

- `NewMaxProgressBar`, `NewSliceProgressBar`, and `NewParallelProgressBar` tolerate non-TTY output and degrade to no-op updates.
- `Close()` stops rendering, erases every row of the bar and releases its terminal resources.
- Progress bars can share the default output with `Input` and `Password`, including their validation feedback. They do not change terminal input mode, so closing a bar leaves an active prompt's editing functional.

For a runnable example combining background progress and validated prompts, run
`go run ./examples/input-validation-progress` from the repository root. See
[Input and Password](input.md#withvalidation) for validation options.
