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
tui.WithLabelTemplate(`{{ green "Select:" }} {{ . | bold }}`)
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
tui.WithActiveItemTemplate(`{{ brightCyan "> " (label .) }}`)
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
tui.WithActiveItemTemplate(`{{ bgBlue " " }} {{ label . }}`)
```

## Text styles

- `bold`
- `dim`
- `italic`
- `underline`
- `strike`

Example:

```go
tui.WithAnswerTemplate(`{{ dim "selected:" }} {{ bold (label .Answer) }}`)
```

## Time formatter

- `ago`

Renders relative time such as `12sec ago`, `3hr ago`, or `in 2min`.

Example:

```go
tui.WithActiveItemTemplate(`{{ .Name }} {{ dim (ago .UpdatedAt) }}`)
```

## Choice label helper

Dropdown and multichoice templates also expose:

- `label`

`label` resolves a display label using tags, common field names, `fmt.Stringer`, then `fmt.Sprint`.

Example:

```go
tui.WithActiveItemTemplate(`{{ green "> " (label .) }}`)
```

## Input feedback templates

`Input` and `Password` accept `WithFeedbackTemplate`. Its data contains only
`.Message`, the sanitized validation error; it does not include the typed answer.
Color and style helpers work here too:

```go
tui.WithFeedbackTemplate(`{{ yellow (bold "Check: ") .Message }}`)
```

Feedback occupies one row. See [Input and Password](input.md#withfeedbacktemplate)
for the default template and clipping behavior.

## Adding custom functions

Use `WithFn(name, fn)` to register your own global helper.

```go
tui.WithFn("upper", strings.ToUpper)
```

Multichoice item templates receive raw items (map entries have `.Key` and
`.Value`). Its answer template receives `.Label`, `.Count`, and the ordered
`.Answer` collection. Use `{{range .Answer}}{{label .}}{{else}}none{{end}}`
for collection answers. See [Multichoice](multichoice.md) for the shared content
templates and default matching contract.
