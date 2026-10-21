---
title: "Migrating to v0.4"
weight: 90
---

v0.4.0 makes the [theme](themes.md) the only way the library draws. The
per-call presentation options, the fixed color names and the per-spinner frames
are removed, and the default colors change. Most breaks are compile errors; one
is not (see [Color names](#color-names)).

## Summary

| v0.3.x | v0.4.0 |
| --- | --- |
| `WithLabelTemplate(t)` | `theme.Templates.<Widget>.Label` |
| `WithActiveItemTemplate(t)`, `WithInactiveItemTemplate(t)` | `theme.Templates.<Widget>.Active`, `.Inactive` (Multichoice: `.Row`) |
| `WithMoreItemsTemplate(t)` | `theme.Templates.<Widget>.More` |
| `WithAnswerTemplate(t)` | `theme.Templates.<Widget>.Answer` (Multichoice: `.AnswerValue`) |
| `WithFeedbackTemplate(t)` | `theme.Templates.Input.Feedback` |
| `WithFieldTemplate("A", "B")` | ``WithTemplate(`.A ", " .B`)`` |
| `WithFrames(f)` | `theme.Symbols.Spinner = f` |
| `SpinnerStyle.Frames`, `SpinnerStyleDocs`, `DefaultSpinnerStyle` | `theme.Symbols.Spinner`; `SpinnerStyle` keeps `Interval` |
| `{{ red … }}`, `{{ bgBlue … }}` and the other 22 color names | roles, `fg`, `bg` |
| default colors | Go brand palette |

`<Widget>` is `Dropdown`, `Multichoice`, `Confirm`, `FilePicker`, `Input`,
`Password` or `Number`, whichever you called the option on. A slot sees a view
of the prompt, such as `.Label`, `.Value` or `.Message`, not the raw payloads
the options took; see [Themes](themes.md#templates) for the fields of each.

## Per-call templates

The six template options are removed, so the call no longer compiles. Move the
look into a theme and install it with `SetDefaultTheme`.

Before:

```go
name, err := tui.Dropdown(ctx, "Fruit", fruits,
	tui.WithLabelTemplate(`{{ cyan "Select:" }} {{ . | bold }}`),
	tui.WithActiveItemTemplate(`{{ green "> " (label .) }}`),
)
```

After:

```go
th := tui.DefaultTheme()
th.Templates.Dropdown.Label = `{{ accent "Select:" }} {{ .Label | bold }}`
th.Templates.Dropdown.Active = `{{ success "> " .Value }}`
if err := tui.SetDefaultTheme(th); err != nil {
	return err
}
name, err := tui.Dropdown(ctx, "Fruit", fruits)
```

Two prompts in one process can no longer have two looks at once. To give one
prompt its own look, swap the default theme around it; running prompts keep the
theme they started with:

```go
prev := tui.DefaultTheme()
defer tui.SetDefaultTheme(prev)
th := prev
th.Templates.Confirm.Answer = tui.Hidden
tui.SetDefaultTheme(th)
```

To hide a prompt's answer line, `WithHide` is simpler.

**Matching changed.** Search and filtering used the output of the inactive-row
template, decoration included. They now match the projected content, the text
`WithTemplate` produces. Use `WithTemplate` to choose what users can search on.

An invalid template fails when the theme is installed, with a path such as
`theme.Templates.Dropdown.Active`.

## `WithFieldTemplate`

The second argument of `WithTemplate` is the details shown on the active row,
so `WithFieldTemplate("A", "B")` is not `WithTemplate(".A", ".B")`. It is:

```go
// before
tui.WithFieldTemplate("Name", "Color")
// after
tui.WithTemplate(`.Name ", " .Color`)
```

## Spinner frames

Before:

```go
spinners.Add(ctx, tui.WithFrames([]string{"-", "\\", "|", "/"}))
```

After, for all spinners:

```go
th := tui.DefaultTheme()
th.Symbols.Spinner = []string{"-", `\`, "|", "/"}
tui.SetDefaultTheme(th)
```

Every frame is a symbol: exactly one column wide, with no control characters.
A frame that is wider, empty of width or multi-line fails `SetDefaultTheme` with
`theme.Symbols.Spinner[i]`. Frames are colored by the spinner templates, not by
escape codes inside the frames. The default frames are now one column wide: a
block snake on Unicode terminals and `|/-\` with `ASCIISymbols()`.

## Color names

The 24 fixed colors (`black` … `white`, `brightBlack` … `brightWhite`,
`bgBlack` … `bgWhite`) are removed. `bold`, `dim`, `italic`, `underline` and
`strike` stay.

| Before | After |
| --- | --- |
| `{{ green "ok" }}` | `{{ success "ok" }}` |
| `{{ red "failed" }}` | `{{ error "failed" }}` |
| `{{ cyan .Name }}` | `{{ accent .Name }}` |
| `{{ yellow "!" }}`, `{{ white .X }}` | `{{ text … }}`, or `{{ fg "#FDDD00" "!" }}` |
| `{{ brightBlack .Note }}` | `{{ muted .Note }}` |
| `{{ bgBlue " " }}` | `{{ bg "#0000AA" " " }}` |

Roles follow the palette and the background; `fg` and `bg` take `#RRGGBB` or
`#RGB`. See [Template Functions](template-functions.md#colors).

**This one fails at run time.** Templates are parsed when a prompt or table
runs, not when your program is built. A template that still uses an old name
fails with the path of the template:

```
theme.Templates.Dropdown.Active: template: Dropdown.Active:1: function "red" not defined
table: row template: template: row:1: function "red" not defined
table: column "Name" template: template: column:1: function "red" not defined
WithTemplate content: template: content:1: function "red" not defined
```

Search your sources for the names before upgrading, for example with
`grep -rnE '\{\{[^}]*\b(red|green|yellow|blue|purple|cyan|white|black|bright[A-Z]\w*|bg[A-Z]\w*)\b'`.
Names are free again: you can register your own `red` with `WithFn`.

## Default colors changed

Callers who left the palette unset see new colors. Screenshots and golden
outputs that contain SGR codes change.

| Role | v0.3.x | v0.4.0 |
| --- | --- | --- |
| `Error` | `#C83741` | Fuchsia `#CE3262` |
| `Light.Foreground` | `#232830` | Black `#000000` |
| `Dark.Foreground` | `#E1E6EE` | Cool Gray `#DBD9D6` |
| `Accent` on light backgrounds | `#00ADD8`, darkened to be readable | dark blue `#00758D` |

`Accent` on dark backgrounds and `Success` are unchanged. To keep the old
colors, set them explicitly:

```go
th := tui.DefaultTheme()
th.Palette.Accent = "#00ADD8"
th.Palette.Error = "#C83741"
th.Palette.Light.Foreground = "#232830"
th.Palette.Dark.Foreground = "#E1E6EE"
tui.SetDefaultTheme(th)
```

Setting `Accent` also turns the dark-blue seed off. See
[Brand colors](themes.md#brand-colors).

## Known gap: native Windows

No Windows host was available while the theme was built. `GOOS=windows` builds
and vets, and the tests compile, but these were not run on a console: enabling
and restoring virtual terminal output when prompts and activity share a
terminal, background detection (it is off on Windows, so the theme uses
`COLORFGBG` or falls back to dark) and resize. Report anything that looks
wrong there.
