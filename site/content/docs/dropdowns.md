---
title: "Dropdowns"
weight: 30
---

## Dropdown

Selects a value from a typed list.

```go
cities := []string{"Amsterdam", "Berlin", "Paris"}
city, err := tui.Dropdown(ctx, "City", cities)
if err != nil {
	return err
}
fmt.Println(city)
```

Use this for the common case where you need the selected value.

## DropdownIndex

Like `Dropdown`, but returns the selected index in `[]any`.

```go
items := []any{"dev", "staging", "prod"}
idx, err := tui.DropdownIndex(ctx, "Environment", items)
if err != nil {
	return err
}
fmt.Println("selected index:", idx)
```

Use this when you want to keep value lookup separate.

## DropdownKV

Selects from a map and returns both key and value.

```go
ports := map[string]int{
	"http":  80,
	"https": 443,
}
name, port, err := tui.DropdownKV(ctx, "Port", ports)
if err != nil {
	return err
}
fmt.Println(name, port)
```

The map is sorted by key string representation before rendering.

## DropdownLazy

Streams items progressively via `iter.Seq2`.

```go
seq := func(yield func(string, error) bool) {
	for i := 1; i <= 100; i++ {
		if !yield(fmt.Sprintf("item-%03d", i), nil) {
			return
		}
	}
}
selected, err := tui.DropdownLazy(ctx, "Pick one", seq)
if err != nil {
	return err
}
fmt.Println(selected)
```

Use this for large or slow data sources.

## Confirm

Renders a yes/no dropdown and returns `true` for "yes".

```go
ok := tui.Confirm(ctx, "Delete cache?")
if !ok {
	return nil
}
```

## Confirmf

Formatted variant of `Confirm`.

```go
ok := tui.Confirmf(ctx, "Delete %d files?", 12)
if !ok {
	return nil
}
```

## WithDefault

Sets a match hint, not a pre-filled filter. The dropdown ranks items by similarity to the hint and returns early only when exactly one item matches it as a prefix; the typed filter stays empty. Also useful with `WithOneReturn`.

```go
city, err := tui.Dropdown(ctx, "City", cities,
	tui.WithDefault("am"),
)
```

A default can also be a typed value. It is kept as is, never turned into a
label: `Dropdown[T]`, `DropdownLazy[T]` and `DropdownIndex` take the item,
`DropdownKV` takes the **key** (a value of the map is not looked up). It must be
assignable to the item type, without conversions, and is compared with `==`, or
with `reflect.DeepEqual` for slices and maps; pointers compare by identity. It
must identify exactly one item: a value that is missing, repeated, a non-nil
function or `NaN` fails with `ErrInvalidOption`. An untyped `nil` matches a nil
item. Items with identical labels stay distinct.

```go
type Server struct{ Name, Region string }

srv, err := tui.Dropdown(ctx, "Server", servers,
	tui.WithDefault(Server{"api", "eu"}),
)
key, value, err := tui.DropdownKV(ctx, "Port", ports, tui.WithDefault("https"))
```

With a terminal the typed item is the first row and Enter confirms it. A string
that is not an item keeps working as the hint above.

### Without a terminal

When the output is not a terminal, every dropdown answers from [`WithDefault`](options-and-helpers.md#defaults-without-a-terminal)
without reading input or printing anything, and fails otherwise:

- no default: an error wrapping `ErrNoTTY`; `DropdownLazy` does not start its
  producer;
- a missing, ambiguous or wrongly typed default: `ErrInvalidOption`;
- a string is first an exact item, so `"a"` selects `"a"` even when `"ab"` exists;
  otherwise it is a word-prefix hint that must match exactly one label;
- with [`WithOneReturn`](#withonereturn), a single item is returned without a default, ahead of any hint.

`DropdownLazy` consumes the whole sequence first, so a later duplicate or a late
producer error is not hidden; an empty sequence is `ErrEmptyLazyResult`.

`Confirm` keeps its `bool` result and cannot show errors: [`WithDefault(true)`](options-and-helpers.md#defaults-without-a-terminal) or
`"Yes"` returns true, `WithDefault(false)` or `"No"` false, and no default, an
invalid default or any error returns false. Call `Dropdown` with `"Yes"` and
`"No"` to see `ErrNoTTY`.

## WithOneReturn

Returns immediately if exactly one item is available.

```go
city, err := tui.Dropdown(ctx, "City", []string{"Amsterdam"},
	tui.WithOneReturn(),
)
```

## WithHide

Hides the final answer line after selection.

```go
city, err := tui.Dropdown(ctx, "City", cities,
	tui.WithHide(),
)
```

## WithTemplate

Sets main and active-details templates in one call.

```go
type City struct {
	Name    string
	Country string
}

city, err := tui.Dropdown(ctx, "City", data,
	tui.WithTemplate(".Name", ".Country"),
)
```

Use this to choose the fields an item shows.

## Presentation

A dropdown draws through the theme. Set the slots once with
[SetDefaultTheme](themes.md); the same slots serve `Confirm` and `FilePicker`
unless a family slot overrides them.

| Slot | Payload | Draws |
| --- | --- | --- |
| `Templates.Dropdown.Label` | `.Label` | the prompt label |
| `Templates.Dropdown.Active` | `.Value`, `.Details`, `.Index` | the focused row |
| `Templates.Dropdown.Inactive` | `.Value`, `.Index` | the other rows |
| `Templates.Dropdown.Hint` | a list with `.Left`, `.Right` | the key hints, led by the count of hidden items |
| `Templates.Dropdown.Answer` | `.Label`, `.Value` | the confirmation line |

```go
th := tui.DefaultTheme()
th.Templates.Dropdown.Label = `{{ accent "Select:" }} {{ bold .Label }}`
th.Templates.Dropdown.Active = `{{ success .Value }}`
th.Templates.Dropdown.Hint = `{{ range . }}{{ .Left }}={{ .Right }} {{ end }}`
th.Templates.Dropdown.Answer = `{{ success "selected:" }} {{ .Value }}`
if err := tui.SetDefaultTheme(th); err != nil {
	return err
}
city, err := tui.Dropdown(ctx, "City", cities)
```

`.Value` is the item as projected by `WithTemplate`, so a struct item needs a
projection to show its fields.

## WithInput

Overrides input source.

```go
city, err := tui.Dropdown(ctx, "City", cities,
	tui.WithInput(in),
)
```

## WithOutput

Overrides output target.

```go
city, err := tui.Dropdown(ctx, "City", cities,
	tui.WithOutput(out),
)
```

## WithOptions

Bundles multiple options for reuse.

```go
opts := tui.WithOptions(
	tui.WithHide(),
)

city, err := tui.Dropdown(ctx, "City", cities, opts)
```

## Default appearance

A dropdown draws through the installed theme:
the `Templates.Dropdown` slots, which inherit from `Templates.Prompt` and
`Templates.Choice`. See [Themes](themes.md) to change it for every widget.

## Label resolution for struct items

`Dropdown` resolves labels in this order:

1. Explicit label tags (`header:"label"`, `tui:"label"`, or `label:"true"`)
2. Heuristic field names (`Label`, `Name`, `Title`, `Description`, `DisplayName`, `FullName`, `Summary`, `Text`, `Subject`)
3. `fmt.Stringer`
4. `fmt.Sprint`
