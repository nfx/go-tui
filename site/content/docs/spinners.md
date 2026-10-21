---
title: "Spinners"
weight: 60
---

APIs:

- `NewSpinners(ctx, opts...)`
- `(*Spinners).Add(ctx, opts...)`
- `(*Spinners).MustAddBackground(opts...)`
- `(*Spinners).Close()`
- `(*Spinner).Update(msg)`
- `(*Spinner).Updatef(format, args...)`
- `(*Spinner).Fail(err)`
- `(*Spinner).Close()`

## Basic example

```go
group, err := tui.NewSpinners(ctx)
if err != nil {
	return err
}
defer group.Close()

sp, err := group.Add(context.Background())
if err != nil {
	return err
}
sp.Update("fetching")
sp.Update("processing")
sp.Close()
```

## Without a TTY

When spinner output has no TTY, stage changes are logged through `slog.Default()`
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
that the work succeeded. `WithKeep` affects terminal display only.

The configured default logger controls the destination, format, and level
filtering. Without this option, `NewSpinners` returns `ErrNoTTY` on non-TTY
output. Other initialization errors are still returned with the option enabled.

## WithPrefixf

Sets spinner prefix label (applies to `Add` and `MustAddBackground`).

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

## WithFrames

Overrides spinner animation frames for a spinner instance.

```go
sp, err := group.Add(context.Background(),
	tui.WithFrames([]string{"-", "\\", "|", "/"}),
)
```

Detailed behavior:

- Frames are cycled in order on each spinner tick.
- Ticks are driven by the spinner group ticker, every `Theme.Spinner.Interval`
  (100ms by default).
- After the last frame, animation loops back to the first frame.
- New spinners use `Theme.Spinner.Frames`; see [Themes](themes.md).
- You can switch one spinner to `DefaultSpinnerStyle` or any custom frame slice.
- `Add` returns an error unless the frames are non-empty single lines of
  text, optionally styled with color sequences, that all have the same
  positive display width. Equal widths keep the animation from jittering.

## WithInput

Overrides group input source.

```go
group, err := tui.NewSpinners(tui.WithInput(in))
```

## WithOutput

Overrides spinner output writer.

```go
group, err := tui.NewSpinners(tui.WithOutput(out))
```

## WithOptions

Composes group options for reuse.

```go
common := tui.WithOptions(
	tui.WithOutput(out),
)

group, err := tui.NewSpinners(common)
```

## Styles

- `SpinnerStyleDocs` has the same frames as the default theme.
- `DefaultSpinnerStyle` is a denser unicode animation set.
- Changing these variables does not change the default; set
  `Theme.Spinner.Frames` instead.

## Failure handling

`Fail(err)` marks spinner as failed and keeps visible error message.

## Sharing a terminal with prompts

Spinners can share the default terminal output with `Input` and `Password`,
including their validation feedback. Spinner updates continue while a validator
runs. Closing a spinner group releases its rendering resources without changing
the active prompt's terminal input mode.

Route accompanying logs through [`tui.Stderr()`](options-and-helpers.md#stderr)
so they participate in the same rendering coordination. See
[Input and Password](input.md#withvalidation) for validation and feedback options.
