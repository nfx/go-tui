---
title: "Template Functions"
weight: 25
---

This page lists predefined helper functions available in widget templates.

## Foreground colors

- `black`
- `red`
- `green`
- `yellow`
- `blue`
- `purple`
- `cyan`
- `white`

Example:

```go
theme.Templates.Dropdown.Label = `{{ green "Select:" }} {{ .Label | bold }}`
```

## Bright foreground colors

- `brightBlack`
- `brightRed`
- `brightGreen`
- `brightYellow`
- `brightBlue`
- `brightPurple`
- `brightCyan`
- `brightWhite`

Example:

```go
theme.Templates.Dropdown.Active = `{{ brightCyan "> " .Value }}`
```

## Background colors

- `bgBlack`
- `bgRed`
- `bgGreen`
- `bgYellow`
- `bgBlue`
- `bgPurple`
- `bgCyan`
- `bgWhite`

Example:

```go
theme.Templates.Dropdown.Active = `{{ bgBlue " " }} {{ .Value }}`
```

## Text styles

- `bold`
- `dim`
- `italic`
- `underline`
- `strike`

Example:

```go
theme.Templates.Dropdown.Answer = `{{ dim "selected:" }} {{ bold .Value }}`
```

## Time formatter

- `ago`

Renders relative time such as `12sec ago`, `3hr ago`, or `in 2min`.

Example:

```go
tui.WithTemplate(`.Name`, `(ago .UpdatedAt)`) // details show on the active row
```

## Choice label helper

Dropdown and multichoice templates also expose:

- `label`

`label` resolves a display label using tags, common field names, `fmt.Stringer`, then `fmt.Sprint`.

Example:

```go
theme.Templates.Dropdown.Active = `{{ green "> " .Value }}`
```

## Input feedback templates

The `Feedback` slot of `Input` and `Password` contains only
`.Message`, the sanitized validation error; it does not include the typed answer.
Color and style helpers work here too:

```go
theme.Templates.Input.Feedback = `{{ yellow (bold "Check: ") .Message }}`
```

Feedback occupies one row. See [Input and Password](input.md#feedback-row)
for the default template and clipping behavior.

## Adding custom functions

Add helpers to `Theme.Funcs` and install the theme with `SetDefaultTheme`,
which returns an error for an invalid helper; see [Themes](themes.md).
`WithFn(name, fn)` adds one helper to the installed theme.

```go
tui.WithFn("upper", strings.ToUpper)
```

The helper applies to prompts, tables, and other operations started after the
call; an operation already running keeps its helpers. Registering the same name
again replaces the helper. `fn` must return one value, or a value and an error.
`WithFn` panics immediately on an invalid name or signature, or on the name of
a built-in helper such as `red`, `bold`, or `label`, and leaves the installed
theme unchanged. Earlier versions only failed later, when a template was
parsed. A later `SetDefaultTheme` replaces helpers registered with `WithFn`
unless its theme started from `DefaultTheme()`. Helpers must be safe for concurrent
use.

Color helpers nest: `{{ red (bold "Error:") " details" }}` keeps the text after
the inner span red. Raw SGR sequences returned by your own helpers are not
adjusted to the terminal's color support.

Multichoice item templates receive raw items (map entries have `.Key` and
`.Value`). Its answer template receives `.Label`, `.Count`, and the ordered
`.Answer` collection. Use `{{range .Answer}}{{label .}}{{else}}none{{end}}`
for collection answers. See [Multichoice](multichoice.md) for the shared content
templates and default matching contract.
