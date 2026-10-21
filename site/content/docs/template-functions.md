---
title: "Template Functions"
weight: 25
---

This page lists predefined helper functions available in widget templates.

## Colors

Colors come from the theme's palette, so they follow the terminal background
and the color support of the output.

Roles:

- `text`
- `muted`
- `accent`
- `success`
- `error`

Exact colors:

- `fg "#RRGGBB" ...` sets the foreground.
- `bg "#RRGGBB" ...` sets the background.

Both accept the `#RGB` shorthand. A malformed color is reported when the
template runs; a literal one fails `SetDefaultTheme`. Exact colors are fitted
to the terminal's profile but not adjusted to its background; prefer a role
when the text must stay readable.

Example:

```go
theme.Templates.Dropdown.Label = `{{ accent "Select:" }} {{ .Label | bold }}`
theme.Templates.Dropdown.Active = `{{ bg "#003B4D" " " }} {{ .Value }}`
```

The 24 fixed-color names of earlier versions (`red`, `green`, `brightCyan`,
`bgBlue` and so on) are removed. A template that uses one fails with
`function "red" not defined` and the path of the template. See the
[v0.4.0 migration notes](migration-v0.4.md).

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
theme.Templates.Dropdown.Active = `{{ success "> " .Value }}`
```

## Input feedback templates

The `Feedback` slot of `Input` and `Password` contains only
`.Message`, the sanitized validation error; it does not include the typed answer.
Color and style helpers work here too:

```go
theme.Templates.Input.Feedback = `{{ error (bold "Check: ") .Message }}`
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
a built-in helper such as `accent`, `bold`, or `label`, and leaves the installed
theme unchanged. Earlier versions only failed later, when a template was
parsed. A later `SetDefaultTheme` replaces helpers registered with `WithFn`
unless its theme started from `DefaultTheme()`. Helpers must be safe for concurrent
use.

Color helpers nest: `{{ error (bold "Error:") " details" }}` keeps the text after
the inner span in the error color. Raw SGR sequences returned by your own helpers are not
adjusted to the terminal's color support.

Multichoice item templates receive raw items (map entries have `.Key` and
`.Value`). Its answer template receives `.Label`, `.Count`, and the ordered
`.Answer` collection. Use `{{range .Answer}}{{label .}}{{else}}none{{end}}`
for collection answers. See [Multichoice](multichoice.md) for the shared content
templates and default matching contract.
