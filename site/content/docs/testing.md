---
title: "Testing in Other Terminals"
weight: 110
---

Color depth, symbols and whether prompts are interactive all come from the
environment. Setting a few variables lets you preview, from your own terminal,
how a program looks and behaves for someone else. Every recipe below works
with any program, for example:

```sh
TERM=linux COLORTERM= go -C examples run ./theme
```

These variables are read once, when the program starts. 

There are examples in the source repository to try these recipes on. They are
a Go module of their own, in the `examples` directory, so the commands on this
page run them from the repository root with `go -C examples`.

## Color depth

| Looks like | Variables |
| --- | --- |
| iTerm2, Ghostty, kitty, WezTerm, Windows Terminal: 24-bit color | `COLORTERM=truecolor` |
| macOS Terminal: 256 colors | `TERM=xterm-256color COLORTERM=` |
| A basic xterm: 16 colors | `TERM=xterm COLORTERM=` |
| A dumb terminal: no color, no attributes | `TERM=dumb COLORTERM=` |
| A user who turned color off: bold and other attributes only | `NO_COLOR=1` |
| Any depth, chosen directly | `FORCE_COLOR=1` (16), `2` (256), `3` (24-bit), `0` (none) |

Clear `COLORTERM` when you aim for fewer colors, since your own terminal
likely sets it. `FORCE_COLOR` wins over every other variable, `NO_COLOR` and
`CI` included, and also colors output to pipes and files. `CLICOLOR_FORCE=1`
colors pipes and files too, at the depth `TERM` and `COLORTERM` suggest. With
16 colors, your terminal draws them from its own palette, not the palette of
the terminal you preview.

## Symbols

| Looks like | Variables |
| --- | --- |
| A UTF-8 terminal: Unicode symbols | `LC_ALL=en_US.UTF-8` |
| The Linux virtual console: 16 colors, ASCII symbols | `TERM=linux COLORTERM=` |
| A non-UTF-8 locale: ASCII symbols | `LC_ALL=en_US.ISO-8859-1` |
| The legacy Windows console: 16 colors, ASCII symbols | `TERM=xterm COLORTERM= LC_ALL=en_US.CP437` |

The locale comes from the first of `LC_ALL`, `LC_CTYPE` and `LANG` that is
set, so `LC_ALL` overrides whatever your shell exports. See
[Symbols](../themes/#symbols) for the full rule, including Windows.

## Interactivity

| Looks like | Command |
| --- | --- |
| A CI log: Unicode symbols, no color, prompts answer their defaults | `CI=true go -C examples run ./number` |
| A CI log that opts into color | `CI=true FORCE_COLOR=3 go -C examples run ./number` |
| A script that pipes the prompts away | `go -C examples run ./number 2>&1 \| cat` |
| A script that discards the prompts | `go -C examples run ./number 2>/dev/null` |

Without a terminal, a prompt with `WithDefault` prints nothing and returns
its default, and one without it fails with `no tty`. Spinners and progress
bars draw nothing and log their updates through `slog.Default()` instead. See
[Defaults without a terminal](../options-and-helpers/#defaults-without-a-terminal)
and [Progress bars without a terminal](../progressbar/#without-a-terminal).

`CI` counts as set for any value but an empty one, `0` and `false`, so
`CI=0` previews an ordinary terminal even inside a CI job. Prompts draw on
stderr, so it is stderr that must leave the terminal: `| cat` alone pipes
only stdout and keeps the prompts interactive.

## Background

| Looks like | Variables |
| --- | --- |
| A light background | `COLORFGBG=0;15` |
| A dark background | `COLORFGBG=15;0` |

Quote the value in a shell, as in `COLORFGBG='0;15'`, since `;` ends a
command. `COLORFGBG` applies to spinners, progress bars and tables. A prompt
asks the terminal for its background, so your own terminal decides how a
prompt looks. Set `theme.Light = true` to preview prompts on a light
background.

## Combining them

The variables are independent, so one preview can combine several:

```sh
# a light 256-color terminal without Unicode fonts
TERM=xterm-256color COLORTERM= LC_ALL=en_US.ISO-8859-1 COLORFGBG='0;15' go -C examples run ./table

# a CI log with color, as GitHub Actions shows it with FORCE_COLOR set
CI=true FORCE_COLOR=3 go -C examples run ./progress-bar
```
