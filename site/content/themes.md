---
title: "Themes"
weight: 160
---

Every widget draws through one theme: prompts, choice lists, [spinners](spinners.md),
[progress bars](progressbar.md), [tables](table.md), [facts](table.md) and [pretty JSON](json.md). Configure it once at startup
and all of them change together.

API:

- `theme.New() theme.Theme` returns the built-in theme to edit. The types of a
  theme live in the `github.com/nfx/go-tui/theme` package.
- `tui.DefaultTheme() theme.Theme` returns a copy of the installed theme.
- `tui.SetDefaultTheme(th theme.Theme) error` validates and installs a theme.
- `th.Normalize()` validates a theme and fills every zero field with its
  default, and `th.Symbols.ASCII()` returns the [ASCII](https://en.wikipedia.org/wiki/ASCII) counterpart of the symbols.

```go
th := theme.New()
th.Palette.Accent = "#00ADD8"
if err := tui.SetDefaultTheme(th); err != nil {
	log.Fatal(err)
}
```

See `examples/theme/basic` for a runnable program.

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

Colors are [CSS](https://en.wikipedia.org/wiki/CSS) hex strings of type `RGB`, such as `"#00ADD8"` or `"#0AD"`.

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
- a locale whose character set is not [UTF-8](https://en.wikipedia.org/wiki/UTF-8), from the first of `LC_ALL`, `LC_CTYPE` and `LANG` that is set, such as
  `en_US.ISO-8859-1`; `C`, `POSIX` and a locale without a character set do not count;
- on Windows, the legacy console host: every terminal except Windows Terminal (`WT_SESSION`), VS Code, JetBrains IDEs, Cmder, Alacritty, rxvt-unicode and a `TERM` of `xterm-256color`, unless `CI` is set.

## Previewing other terminals

Color depth and symbols come from the environment, so a program can be previewed as it looks elsewhere without
leaving your terminal. See [Testing](testing.md) for the variables to set.

## Templates

Each widget owns its template slots beside its settings: `theme.Progress.Frame` sits with `theme.Progress.Compact`, and `theme.Table.Header` with `theme.Table.ColumnGap`. The slots are the label, the answer, the key hint and the feedback of a prompt, the rows of a choice list, the states of a spinner, the parts of a progress bar and the header and cells of a table.

Slots inherit: `Prompt` applies to [Input](input.md), [Password](input.md), [Number](number.md),
[Dropdown](dropdowns.md#dropdown), [Multichoice](multichoice.md), [Confirm](dropdowns.md#confirm) and [FilePicker](filepicker.md#filepicker), and `Choice` to every
choice list. A family slot such as `Dropdown.Label` overrides only
that family. An empty `Template` inherits; `theme.Hidden` hides an optional slot,
such as the key hint:

```go
th.Prompt.Hint = theme.Hidden
th.Multichoice.Row = `{{ if .Active }}→{{ else }} {{ end }} ` +
	`[{{ if .Selected }}x{{ else }} {{ end }}] {{ .Value }}`
```

The key hint receives a list of hints, each with `.Left` and `.Right`: a key
and what it does, such as `↑↓` and `move`. A choice list with items below
the window leads with their count, as in `3 more · ↑↓ move · type filter`.
A hidden hint hides the count too.

```go
th.Prompt.Hint = `{{ range . }}[{{ .Left }}] {{ .Right }} {{ end }}`
```

Every slot renders on one row; line breaks or cursor movement in its output
are an error. Slot templates can use the helpers described in
[Template functions](#template-functions).

## Template functions

### Color roles

- `text`
- `muted`
- `guide` (the faintest, as for the guide symbol)
- `accent`
- `success`
- `error`

Roles follow the theme palette and the terminal background, so a theme can
recolor every template at once.

```go
th.Dropdown.Label = `{{ accent "Select:" }} {{ .Label | bold }}`
```

`symbol "Focus"` and the other symbol names render a theme symbol.

### Exact colors

- `fg "#RRGGBB" ...`
- `bg "#RRGGBB" ...`

Both accept hex `#RRGGBB` or `#RGB`, validated when the template runs, and are
adapted to the terminal's color support.

```go
th.Dropdown.Active = `{{ bg "#005F87" " " }} {{ fg "#FFFFFF" .Value }}`
```

The fixed 16-color names (`red`, `green`, `brightCyan`, `bgBlue`, and so on)
are gone. A template that still uses one fails with `function "red" not
defined` and the path of the template, when it is installed or parsed.

### Text styles

- `bold`
- `dim`
- `italic`
- `underline`
- `strike`

```go
th.Dropdown.Answer = `{{ dim "selected:" }} {{ bold .Value }}`
```

Color helpers nest: `{{ error (bold "Error:") " details" }}` keeps the text after
the inner span in the error color. Raw [SGR](https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_(Select_Graphic_Rendition)_parameters) sequences returned by your own helpers are not
adjusted to the terminal's color support.

### Time formatter

- `ago`

Renders relative time such as `12sec ago`, `3hr ago`, or `in 2min`.

```go
tui.WithTemplate(`.Name`, `(ago .UpdatedAt)`) // details show on the active row
```

### Choice label helper

[Dropdown](dropdowns.md#presentation) and [multichoice](multichoice.md#templates) templates also expose `label`, which resolves a
display label using tags, common field names, `fmt.Stringer`, then `fmt.Sprint`.

```go
th.Dropdown.Active = `{{ accent "> " .Value }}`
```

Multichoice item templates receive raw items (map entries have `.Key` and
`.Value`). Its answer template receives `.Label`, `.Count`, and the ordered
`.Answer` collection. Use `{{range .Answer}}{{label .}}{{else}}none{{end}}`
for collection answers. See [Multichoice](multichoice.md) for the shared content
templates and default matching contract.

### Input feedback templates

The `Feedback` slot of [`Input`](input.md) and [`Password`](input.md) contains only
`.Message`, the sanitized validation error; it does not include the typed answer.
Color and style helpers work here too:

```go
th.Input.Feedback = `{{ error (bold "Check: ") .Message }}`
```

Feedback occupies one row. See [Input and Password](input.md#feedback-row)
for the default template and clipping behavior.

### Custom helpers

Add helpers in `theme.Funcs`. They are validated by `SetDefaultTheme`:

```go
th.Funcs = template.FuncMap{"shout": strings.ToUpper}
th.Prompt.Label = `{{ accent (symbol "Prompt") }} {{ bold (shout .Label) }}`
```

`theme.Funcs` replaces every helper registered before, including through
[`WithFn`](options.md#withfn). Start from `tui.DefaultTheme()` to keep them. Helpers cannot take
the name of a built-in helper.

[`WithFn(name, fn)`](options.md#withfn) adds one helper to the installed theme:

```go
tui.WithFn("upper", strings.ToUpper)
```

The helper applies to prompts, tables, and other operations started after the
call; an operation already running keeps its helpers. Registering the same name
again replaces the helper. `fn` must return one value, or a value and an error.
[`WithFn`](options.md#withfn) panics immediately on an invalid name or signature, or on the name of
a built-in helper such as `accent`, `bold`, or `label`, and leaves the installed
theme unchanged. Earlier versions only failed later, when a template was
parsed. A later `SetDefaultTheme` replaces helpers registered with [`WithFn`](options.md#withfn)
unless its theme started from `DefaultTheme()`. Helpers must be safe for concurrent
use.

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
- Each widget keeps the theme it started with: a running [spinner](spinners.md) group, a
  [FilePicker](filepicker.md#filepicker) moving between directories or a [`TableIter`](table.md#tableiter) never mixes two
  themes.
- Per-call [`WithTemplate`](dropdowns.md#withtemplate) picks the projection; it does not change how a
  slot draws it.
