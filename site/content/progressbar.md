---
title: "Progress Bars"
weight: 70
---

[`NewMaxProgressBar`](#newmaxprogressbar), [`NewSliceProgressBar`](#newsliceprogressbar), and [`NewParallelProgressBar`](#newparallelprogressbar) tolerate non-TTY output and report milestones through `slog` instead of redrawing. `Close()` stops rendering, erases every row of the bar and releases its terminal resources. Progress bars can [share the default output](#several-bars) with [`Input`](input.md), [`Password`](input.md), [`Dropdown`](dropdowns.md), and [`Multichoice`](multichoice.md), including their validation feedback. They do not change terminal input mode, so closing a bar leaves an active prompt's editing functional.

## NewMaxProgressBar

Creates a mutable progress bar with explicit max size. Use this when your total work count is known.

{{< recorded name="progress/progress-bar" title="A progress bar advancing to completion" >}}

## NewSliceProgressBar

Wraps slice iteration and auto-advances progress as items are consumed. Use this for sequential processing loops.

```go
for item, err := range tui.NewSliceProgressBar(ctx, "Items", items) {
	if err != nil {
		return err
	}
	_ = item
}
```



## NewParallelProgressBar

Processes slice items concurrently and updates one shared progress bar.
Returns the first worker error and cancels remaining work.

{{< recorded name="progress/progress-parallel" title="Parallel workers advancing one progress bar" >}}

## NewFileProgressReader

Wraps a reader and advances progress by bytes read.
Requires size discovery through `Stat().Size()` or `Size() int64`.

{{< example name="progress/progress-reader" >}}

Without a terminal, reads continue normally and progress is logged as
`file progress` records with `read` and `size` in bytes, see
[Without a terminal](#without-a-terminal). An empty file logs 100% on [EOF](https://en.wikipedia.org/wiki/End-of-file); an
early EOF does not report unread bytes as complete.

## Without a terminal

When the output is not a terminal, or [`CI` env is set](testing.md#without-a-terminal), a bar draws nothing and
logs its progress through `slog.Default()` at INFO instead. A record is
emitted when the bar reaches a new 10% milestone and includes `label`,
`percent`, `done` and `total`. Large steps log only the latest milestone
reached, so a single step to the end logs 100% once. An empty bar logs 100%
when it closes; a bar closed early does not report the rest as done. Configure
the default logger to choose the destination and format.

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

Sets worker count for [`NewParallelProgressBar`](#newparallelprogressbar). Value must be greater than zero.

```go
err := tui.NewParallelProgressBar(ctx, "Jobs", jobs, runJob,
	tui.WithWorkers(8),
)
```

## Appearance

By default, a bar has two rows: the label, the percentage and the bar, and a
hint row with the metrics below it.

```text
Download  62%  ━━━━━━━━━━━━━━━━━━━╸────────────
62.0 / 100.0 MiB · 6.2 MiB/s · 12s left
```

On a terminal without the Unicode glyphs:

```text
Download  62%  ===================>------------
62.0 / 100.0 MiB | 6.2 MiB/s | 12s left
```

The percentage always takes four columns, so the bar never moves as it
grows. A bar without a total shows `—` (`?` in [ASCII](https://en.wikipedia.org/wiki/ASCII)) instead of a
percentage. The hint row shows the amount, the rate, the time left and, with
the elapsed time enabled, the elapsed time. When the row is too narrow, it
drops whole entries from the end; when even the first entry does not fit,
the hint row goes. A long label is shortened before the bar is. A terminal
too short for both rows gets the compact row. A bar on its own terminal
through `WithOutput` also keeps a row below them for the cursor, so it needs
three.

The hint row is the `Progress.Hint` slot. Like a prompt's key
hint, it receives a list of entries, each with `.Left` and `.Right`, such as
`6.2` and `MiB/s`. It does not inherit from `Prompt.Hint`.

```go
th := theme.New()
th.Progress.Hint = `{{ range . }}[{{ .Left }} {{ .Right }}] {{ end }}`
```

Set it to `theme.Hidden` to keep only the bar row.

### Compact layout

`theme.Progress.Compact = true` keeps everything on one row,
as earlier versions did, and drops metrics from the end to keep the bar:

```text
Download 62%  ━━━━━━━╸──── 6.2 MiB/s · 12s remaining
```

{{< recorded name="progress/progress-multi-compact" title="Several compact progress bars advancing together" >}}

In the stacked layout, `Progress.Frame` receives an empty
`.Metrics`. A custom frame that shows `.Metrics` after the bar keeps
working once it selects the compact layout:

`Progress.Metrics` applies to the compact layout only, and
`Progress.Hint` to the stacked layout only.

## Several bars

Every bar registers with the same coordinator, so goroutines can advance their own bars concurrently.

{{< recorded name="progress/progress-multi" title="Multiple progress bars advancing concurrently" >}}

