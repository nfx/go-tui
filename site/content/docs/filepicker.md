---
title: "File Picker"
weight: 100
---

## FilePicker

Opens an interactive file picker and returns selected path.

```go
path, err := tui.FilePicker("Select config",
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
path, err := tui.FilePicker("Pick file",
	tui.WithStartDir("/var/log"),
)
```

## WithExtensions

Filters non-directory entries by extension (case-insensitive).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithExtensions(".json", ".yaml"),
)
```

## WithIgnoreUp

Hides `..` navigation entry.

```go
path, err := tui.FilePicker("Pick file",
	tui.WithIgnoreUp(),
)
```

## WithIgnoreDirs

Hides directory entries.

```go
path, err := tui.FilePicker("Pick file",
	tui.WithIgnoreDirs(),
)
```

## WithShowHidden

Shows dot-files and dot-directories.

```go
path, err := tui.FilePicker("Pick file",
	tui.WithShowHidden(),
)
```

## WithTemplate

Forwarded to dropdown template configuration. See [WithTemplate](dropdowns.md#withtemplate).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithTemplate(".Name"),
)
```

## WithLabelTemplate

Forwarded to dropdown label template. See [WithLabelTemplate](dropdowns.md#withlabeltemplate).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithLabelTemplate(`{{ cyan "file:" }} {{ . }}`),
)
```

## WithActiveItemTemplate

Forwarded to dropdown active-row template. See [WithActiveItemTemplate](dropdowns.md#withactiveitemtemplate).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithActiveItemTemplate(`{{ green "> " . }}`),
)
```

## WithInactiveItemTemplate

Forwarded to dropdown inactive-row template. See [WithInactiveItemTemplate](dropdowns.md#withinactiveitemtemplate).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithInactiveItemTemplate(`{{ dim "  " . }}`),
)
```

## WithMoreItemsTemplate

Forwarded to dropdown footer template. See [WithMoreItemsTemplate](dropdowns.md#withmoreitemstemplate).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithMoreItemsTemplate(`{{ dim .More " hidden" }}`),
)
```

## WithAnswerTemplate

Forwarded to dropdown answer template. See [WithAnswerTemplate](dropdowns.md#withanswertemplate).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithAnswerTemplate(`{{ green "picked:" }} {{ .Answer }}`),
)
```

## WithHide

Forwarded to dropdown answer visibility. See [WithHide](dropdowns.md#withhide).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithHide(),
)
```

## WithOneReturn

Forwarded to dropdown one-item auto-select behavior. See [WithOneReturn](dropdowns.md#withonereturn).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithOneReturn(),
)
```

## WithDefault

Forwarded to the dropdown as a match hint (not a pre-filled filter). See [WithDefault](dropdowns.md#withdefault).

```go
path, err := tui.FilePicker("Pick file",
	tui.WithDefault("conf"),
)
```

## WithOptions

Bundles picker and forwarded dropdown options.

```go
common := tui.WithOptions(
	tui.WithShowHidden(),
	tui.WithTemplate(".Name"),
)

path, err := tui.FilePicker("Pick file", common)
```

## Option compatibility note

`FilePicker` accepts:

- picker options (`WithStartDir`, `WithExtensions`, `WithIgnoreUp`, `WithIgnoreDirs`, `WithShowHidden`)
- dropdown-targeted options (see links above)

It does not accept shared IO/context options directly (`WithContext`, `WithTimeout`, `WithInput`, `WithOutput`).
