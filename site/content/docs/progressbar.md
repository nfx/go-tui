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

## Behavior notes

- `NewMaxProgressBar`, `NewSliceProgressBar`, and `NewParallelProgressBar` tolerate non-TTY output and degrade to no-op updates.
- `Close()` stops rendering and releases the progress bar's terminal resources.
- Progress bars can share the default output with `Input` and `Password`, including their validation feedback. They do not change terminal input mode, so closing a bar leaves an active prompt's editing functional.

For a runnable example combining background progress and validated prompts, run
`go run ./examples/input-validation-progress` from the repository root. See
[Input and Password](input.md#withvalidation) for validation options.
