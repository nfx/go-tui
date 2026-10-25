---
title: "Spinners"
weight: 80
---

APIs:

- `NewSpinners(ctx, opts...)`
- `(*Spinners).Add(ctx, opts...)`
- `(*Spinners).MustAddBackground(opts...)`
- `(*Spinners).Close()`
- `(*Spinner).Add(ctx, opts...)`
- `(*Spinner).Update(msg)`
- `(*Spinner).Updatef(format, args...)`
- `(*Spinner).Fail(err)`
- `(*Spinner).Close()`

## Basic example

{{< recorded name="spinner/multi-spinners" title="Several spinners updating independently" >}}

## Without a TTY

When spinner output has no [TTY](https://en.wikipedia.org/wiki/Computer_terminal#Text_terminals), stage changes are logged through `slog.Default()`
instead. On a TTY, the group renders animated spinners as usual and emits no
fallback logs.

```go
group, err := tui.NewSpinners(ctx)
if err != nil {
	return err
}
defer group.Close()

sp, err := group.Add(ctx, tui.WithPrefixf("download"))
if err != nil {
	return err
}
sp.Update("fetching")
sp.Update("processing")
sp.Close()
```

Starts, changed `Update`/`Updatef` messages, and stops log at INFO; `Fail(err)`
logs at ERROR. Records include `spinner` (the group's spinner index), `prefix`,
and `status` (`started`, `running`, `stopped`, or `failed`). Repeated identical
updates and animation ticks produce no logs. Failed spinners do not also log a
stop. A stop can result from `Close` or context cancellation; it does not imply
that the work succeeded. [`WithKeep`](#withkeep) affects terminal display only.

The configured default logger controls the destination, format, and level
filtering. Other initialization errors are still returned. Nested spinners add
`parent` (the spinner index of the parent) and `depth` to their records. The
elapsed time is never logged.

## WithPrefixf

Sets spinner prefix label (applies to `Add` and `MustAddBackground`). The
label is formatted as in `fmt.Sprintf` and drawn before the message, in the accent color
while the spinner runs, for example `▘ deploy: updating services (12s)`. A spinner without a label shows
the message alone.

```go
sp, err := group.Add(context.Background(),
	tui.WithPrefixf("worker-%d", 3),
)
```

## WithKeep

Keeps spinner row visible after close.

```go
sp, err := group.Add(context.Background(),
	tui.WithKeep(),
)
```

## WithNoElapsed

Hides the time a spinner has been running. On a terminal each row ends with
the elapsed time in whole seconds (`12s`, `1m05s`, `1h00m00s`), counted from the
creation of the spinner until it stops or fails, so it freezes at the first
ending. Pass it to `NewSpinners` for every spinner of the group, or to `Add`
for one. Without a terminal there is no timer.

```go
group, err := tui.NewSpinners(ctx, tui.WithNoElapsed())
```

## Nested spinners

`(*Spinner).Add` creates a spinner under another one. Children are drawn below
their parent in the order they were added, hanging from branches drawn with
`Theme.Symbols` (`Branch`, `End` and `Guide`).

```text
▘ deploy: updating services (12s)
├ ▀ api: uploading image (8s)
│ └ ▝ layers: pushing layer 3 (2s)
└ ▐ web: restarting (3s)
▗ checking configuration (0s)
```

- Closing, failing or canceling a spinner stops its running children too. A
  child stopping or failing never affects its parent or siblings, and finishing
  the last child does not finish the parent.
- A stopped or failed spinner takes no children: `Add` returns
  [`ErrSpinnerStopped`](options.md#common-errors).
- A kept or failed child stays visible, and so do the stopped ancestors above
  it, drawn as a neutral row.
- The parent's time includes its children's work; it is not their sum.
- Custom [spinner templates](themes.md) may print `.Elapsed` and `.Depth`; the
  group draws the branches whatever the template prints.
- Every spinner takes one row. When the rows do not fit the terminal, the last
  visible row reads `… N more` and stands for the rows after it. They are not
  scrolled, and they show again when the terminal grows or other rows end.

{{< recorded name="spinner/spinner-options" title="Nested spinners with WithKeep and WithNoElapsed" >}}

## LongRunning

`LongRunning(ctx, message, fn)` shows a spinner for as long as the callback runs and returns its error.

{{< recorded name="spinner/long-running" title="A spinner while a callback runs" >}}

## Frames

The frames are `Theme.Symbols.Spinner`. Each frame is a symbol: a single
display column, like every other symbol. The Unicode preset animates a block
around the four quadrants of the cell (`▘▀▝▐▗▄▖▌`) and terminals without the
Unicode glyphs show `|/-\`.

```go
th := tui.DefaultTheme()
th.Symbols.Spinner = []string{"-", "\\", "|", "/"}
err := tui.SetDefaultTheme(th)
```

- Frames are cycled in order on each tick, every `Theme.Spinner.Interval`
  (100ms by default). After the last frame, the animation loops back to the
  first.
- An empty slice takes the preset. A frame that is empty, a control
  character, or not exactly one column wide makes `SetDefaultTheme` fail with
  `theme.Symbols.Spinner[i]`.
- Color the frames with the spinner templates, see [Themes](themes.md), not
  with escape codes inside the frames.
- A spinner group keeps the frames it started with.

## Failure handling

`Fail(err)` marks spinner as failed and keeps visible error message.

## Sharing a terminal with prompts

Spinners can share the default terminal output with [`Input`](input.md) and [`Password`](input.md),
including their validation feedback. Spinner updates continue while a validator
runs. Closing a spinner group releases its rendering resources without changing
the active prompt's terminal input mode.

Route accompanying logs through [`tui.Stderr()`](options.md#stderr)
so they participate in the same rendering coordination. See
[Input and Password](input.md#withvalidation) for validation and feedback options.

{{< recorded name="spinner/multi-spinners-and-logs" title="Spinners and structured logs sharing one terminal" >}}
