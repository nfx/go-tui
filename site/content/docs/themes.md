---
title: "Themes"
weight: 22
---

Every widget draws through one theme: prompts, choice lists, spinners,
progress bars, tables, facts and pretty JSON. Configure it once at startup
and all of them change together.

API:

- `NewTheme() Theme` returns the built-in theme to edit.
- `DefaultTheme() Theme` returns a copy of the installed theme.
- `SetDefaultTheme(theme Theme) error` validates and installs a theme.

```go
theme := tui.NewTheme()
theme.Palette.Accent = "#00ADD8"
if err := tui.SetDefaultTheme(theme); err != nil {
	log.Fatal(err)
}
```

See `examples/theme` for a runnable program.

## Layout

The default layout is Guided: the label of a prompt has its own row, and the
value, the list and the feedback below it start with a guide glyph.

```text
◆ Color
│ ❯ blue
│   green
│ ↑↓ move · type filter
```

Set `theme.Inline = true` to keep the label on the row of the value or the
first item, as earlier versions did. Only the geometry changes: templates,
matching and answers stay the same.

After a prompt is answered, one line stays in the scrollback, such as
`✔ Color · blue`.

## Palette

Colors are CSS hex strings of type `RGB`, such as `"#00ADD8"` or `"#0AD"`.

| Field | Used for |
| --- | --- |
| `Palette.Accent` | the focused row, the prompt symbol, running activity, JSON keys |
| `Palette.Success` | answered prompts and positive values you opt into |
| `Palette.Error` | rejections, failures and negative values you opt into |
| `Palette.Light`, `Palette.Dark` | per-background overrides, including `Foreground`, `Muted` and `Guide` |

The three seeds are shared by light and dark terminals. A seed renders exactly
as given when it is readable on the terminal background; otherwise a readable
shade of the same hue is used. A color set in `Palette.Light` or
`Palette.Dark` always renders exactly as given.

Leave `Foreground`, `Muted` and `Guide` unset to let them follow the
background. While the background is unknown, an unset foreground and muted
color keep the terminal's own colors rather than guess. Set them explicitly,
or set `theme.Light = true`, to use them anyway.

The background is detected when a prompt starts: the terminal is asked for
its color, and `COLORFGBG` is the fallback. Output-only widgets never ask;
they reuse an earlier answer for the same terminal. `theme.Light = true`
skips detection and uses the light palette.

## Color support

The person running the program decides about color, per output:

- A terminal gets color, at the depth it supports.
- Pipes and files get plain text, unless `FORCE_COLOR` or `CLICOLOR_FORCE` is set.
- `NO_COLOR` keeps bold and other attributes, and drops colors.

These variables are read once, when the program starts.

## Symbols

`theme.Symbols` holds every glyph: the prompt, focus and guide marks,
checkboxes, outcome marks, the ellipsis, the password mask, the arrow keys of hints (`Up`, `Down`), the progress
bar cells (`BarFill`, `BarHead`, `BarEmpty`), the mark of an unknown value (`Unknown`), the separator of progress
hints (`Separator`) and the spinner frames (`Spinner`). Each must be one column wide.

Where the environment suggests the terminal font lacks the Unicode glyphs, every symbol left at its preset becomes its
ASCII counterpart, such as `?` for the prompt, `|` for the guide and `|/-\` for the spinner, and symbols the theme sets
itself are kept. These terminals count as lacking them:

- the Linux virtual console (`TERM=linux`);
- a locale whose character set is not UTF-8, from the first of `LC_ALL`, `LC_CTYPE` and `LANG` that is set, such as
  `en_US.ISO-8859-1`; `C`, `POSIX` and a locale without a character set do not count;
- on Windows, the legacy console host: every terminal except Windows Terminal (`WT_SESSION`), VS Code, JetBrains IDEs, Cmder, Alacritty, rxvt-unicode and a `TERM` of `xterm-256color`, unless `CI` is set.

## Previewing other terminals

Color depth and symbols come from the environment, so a program can be previewed as it looks elsewhere without
leaving your terminal:

```sh
TERM=linux COLORTERM= go run ./examples/theme
```

| Looks like | Variables |
| --- | --- |
| iTerm2, Ghostty, kitty, WezTerm, Windows Terminal: 24-bit color | `COLORTERM=truecolor` |
| macOS Terminal: 256 colors | `TERM=xterm-256color COLORTERM=` |
| A basic xterm: 16 colors | `TERM=xterm COLORTERM=` |
| The Linux virtual console: 16 colors, ASCII symbols | `TERM=linux COLORTERM=` |
| The legacy Windows console: 16 colors, ASCII symbols | `TERM=xterm COLORTERM= LC_ALL=en_US.CP437` |
| A CI log: Unicode symbols, no color | `CI=true` |
| A CI log that opts into color | `CI=true FORCE_COLOR=3` |
| A user who turned color off: bold and other attributes only | `NO_COLOR=1` |

`FORCE_COLOR` picks the depth directly and wins over the others: `1` for 16 colors, `2` for 256, `3` for 24-bit
and `0` for none. It also colors output to pipes and files. Clear `COLORTERM` when you aim for fewer colors, since
your own terminal likely sets it. With 16 colors, your terminal draws them from its own palette, not the
palette of the terminal you preview.

`COLORFGBG=0;15` previews a light background for spinners, progress bars and tables, and `COLORFGBG=15;0` a dark
one. A prompt asks the terminal for its background, so your own terminal decides how a prompt looks. Set
`theme.Light = true` to preview prompts on a light background.

## Templates

`theme.Templates` holds one template per slot, such as the label, the answer,
the key hint and the feedback of a prompt, the rows of a choice list, the
states of a spinner, the parts of a progress bar and the header and cells of
a table.

Slots inherit: `Templates.Prompt` applies to Input, Password, Number,
Dropdown, Multichoice, Confirm and FilePicker, and `Templates.Choice` to every
choice list. A family slot such as `Templates.Dropdown.Label` overrides only
that family. An empty `Template` inherits; `tui.Hidden` hides an optional slot,
such as the key hint:

```go
theme.Templates.Prompt.Hint = tui.Hidden
theme.Templates.Multichoice.Row = `{{ if .Active }}→{{ else }} {{ end }} ` +
	`[{{ if .Selected }}x{{ else }} {{ end }}] {{ .Value }}`
```

The key hint receives a list of hints, each with `.Left` and `.Right`: a key
and what it does, such as `↑↓` and `move`. A choice list with items below
the window leads with their count, as in `3 more · ↑↓ move · type filter`.
A hidden hint hides the count too.

```go
theme.Templates.Prompt.Hint = `{{ range . }}[{{ .Left }}] {{ .Right }} {{ end }}`
```

Slot templates can use the role helpers `text`, `muted`, `guide` (the
faintest, as for the guide symbol), `accent`, `success` and `error`, `symbol "Focus"` and the other symbol names, `fg` and `bg` with a
hex color, and the helpers listed in [Template Functions](template-functions.md).
Every slot renders on one row; line breaks or cursor movement in its output
are an error.

## Custom helpers

Add helpers in `theme.Funcs`. They are validated by `SetDefaultTheme`:

```go
theme.Funcs = template.FuncMap{"shout": strings.ToUpper}
theme.Templates.Prompt.Label = `{{ accent (symbol "Prompt") }} {{ bold (shout .Label) }}`
```

`theme.Funcs` replaces every helper registered before, including through
`WithFn`. Start from `tui.DefaultTheme()` to keep them. Helpers cannot take
the name of a built-in helper.

## Activity and static output

| Field | Controls |
| --- | --- |
| `theme.Symbols.Spinner`, `theme.Spinner.Interval` | the frames and step of new spinner groups |
| `theme.Progress.Compact` | keeps the metrics on the row of the bar instead of a hint row below it (the default) |
| `theme.Progress.MinBarWidth`, `MaxBarWidth` | how the bar shares its row with the label and metrics |
| `theme.Table.ColumnGap`, `MinColumnWidth` | table and facts layout |
| `theme.JSON.Indent`, `theme.JSON.Compact` | `PrettyJSON` layout |

## When a theme applies

- `SetDefaultTheme` copies the theme. Editing it afterwards changes nothing.
- An invalid theme returns an error naming the field, and the previous theme
  stays installed.
- Each widget keeps the theme it started with: a running spinner group, a
  FilePicker moving between directories or a `TableIter` never mixes two
  themes.
- Per-call `WithTemplate` picks the projection; it does not change how a
  slot draws it.
