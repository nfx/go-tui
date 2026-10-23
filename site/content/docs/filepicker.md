---
title: "File Picker"
weight: 100
---

## FilePicker

Opens an interactive file picker and returns selected path.

```go
path, err := tui.FilePicker(ctx, "Select config",
	tui.WithStartDir("/etc"),
	tui.WithExtensions(".yaml", ".yml", ".json"),
)
if err != nil {
	return err
}
fmt.Println(path)
```

`FilePicker` is implemented on top of [Dropdown](dropdowns.md#dropdown).

## WithStartDir

Sets initial directory. Default is current working directory.

```go
path, err := tui.FilePicker(ctx, "Pick file",
	tui.WithStartDir("/var/log"),
)
```

## WithExtensions

Filters non-directory entries by extension (case-insensitive).

```go
path, err := tui.FilePicker(ctx, "Pick file",
	tui.WithExtensions(".json", ".yaml"),
)
```

## WithIgnoreUp

Hides `..` navigation entry.

```go
path, err := tui.FilePicker(ctx, "Pick file",
	tui.WithIgnoreUp(),
)
```

## WithIgnoreDirs

Hides directory entries.

```go
path, err := tui.FilePicker(ctx, "Pick file",
	tui.WithIgnoreDirs(),
)
```

## WithShowHidden

Shows dot-files and dot-directories.

```go
path, err := tui.FilePicker(ctx, "Pick file",
	tui.WithShowHidden(),
)
```

## WithTemplate

Forwarded to dropdown template configuration. See [WithTemplate](dropdowns.md#withtemplate).

```go
path, err := tui.FilePicker(ctx, "Pick file",
	tui.WithTemplate(".Name"),
)
```

## Presentation

`FilePicker` draws with the `FilePicker` slots of the theme, which
inherit from the dropdown and choice slots. See
[Presentation](dropdowns.md#presentation).

```go
th := tui.DefaultTheme()
th.FilePicker.Label = `{{ accent "file:" }} {{ .Label }}`
th.FilePicker.Answer = `{{ success "picked:" }} {{ .Value }}`
if err := tui.SetDefaultTheme(th); err != nil {
	return err
}
path, err := tui.FilePicker(ctx, "Pick file")
```

## WithHide

Forwarded to dropdown answer visibility. See [WithHide](dropdowns.md#withhide).

```go
path, err := tui.FilePicker(ctx, "Pick file",
	tui.WithHide(),
)
```

## WithOneReturn

Forwarded to dropdown one-item auto-select behavior. See [WithOneReturn](dropdowns.md#withonereturn).

```go
path, err := tui.FilePicker(ctx, "Pick file",
	tui.WithOneReturn(),
)
```

## WithDefault

Forwarded to the dropdown as a match hint (not a pre-filled filter). See [WithDefault](dropdowns.md#withdefault).

```go
path, err := tui.FilePicker(ctx, "Pick file",
	tui.WithDefault("conf"),
)
```

Without a terminal the picker answers at every directory level from the
default, after the extension, hidden and directory filters. A string is an entry
name first and a prefix hint second; an `os.DirEntry` is found by name and kind.
Other types, and missing or ambiguous entries, fail with `ErrInvalidOption`; a
hint is never taken as a path. A directory continues the walk and only a file is
returned. A directory seen twice is an error, not a loop. Without a default or
[`WithOneReturn`](#withonereturn) the call fails with `ErrNoTTY` before listing anything;
filesystem errors are returned.

## WithOptions

Bundles picker and forwarded dropdown options.

```go
common := tui.WithOptions(
	tui.WithShowHidden(),
	tui.WithTemplate(".Name"),
)

path, err := tui.FilePicker(ctx, "Pick file", common)
```

## Option compatibility note

`FilePicker` accepts:

- picker options (`WithStartDir`, `WithExtensions`, `WithIgnoreUp`, `WithIgnoreDirs`, `WithShowHidden`)
- dropdown-targeted options (see links above)

It does not accept shared IO/context options directly (`WithInput`, `WithOutput`).
