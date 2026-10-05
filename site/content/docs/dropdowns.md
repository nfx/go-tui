---
title: "Dropdowns"
weight: 30
---

## Dropdown

Selects a value from a typed list.

```go
cities := []string{"Amsterdam", "Berlin", "Paris"}
city, err := tui.Dropdown("City", cities)
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
idx, err := tui.DropdownIndex("Environment", items)
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
name, port, err := tui.DropdownKV("Port", ports)
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
selected, err := tui.DropdownLazy("Pick one", seq)
if err != nil {
	return err
}
fmt.Println(selected)
```

Use this for large or slow data sources.

## Confirm

Renders a yes/no dropdown and returns `true` for "yes".

```go
ok := tui.Confirm("Delete cache?")
if !ok {
	return nil
}
```

## Confirmf

Formatted variant of `Confirm`.

```go
ok := tui.Confirmf("Delete %d files?", 12)
if !ok {
	return nil
}
```

## WithDefault

Sets a match hint, not a pre-filled filter. The dropdown ranks items by similarity to the hint and returns early only when exactly one item matches it as a prefix; the typed filter stays empty. Also useful with `WithOneReturn`.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithDefault("am"),
)
```

## WithOneReturn

Returns immediately if exactly one item is available.

```go
city, err := tui.Dropdown("City", []string{"Amsterdam"},
	tui.WithOneReturn(),
)
```

## WithHide

Hides the final answer line after selection.

```go
city, err := tui.Dropdown("City", cities,
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

city, err := tui.Dropdown("City", data,
	tui.WithTemplate(".Name", ".Country"),
)
```

Use this as the preferred template override.

## WithLabelTemplate

Overrides prompt label rendering.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithLabelTemplate(`{{ cyan "Select:" }} {{ . | bold }}`),
)
```

## WithActiveItemTemplate

Overrides active row template.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithActiveItemTemplate(`{{ green "> " (label .) }}`),
)
```

## WithInactiveItemTemplate

Overrides inactive row template.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithInactiveItemTemplate(`{{ dim "  " (label .) }}`),
)
```

## WithMoreItemsTemplate

Overrides the "more items" footer row.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithMoreItemsTemplate(`{{ dim .More " hidden, " .Total " total" }}`),
)
```

## WithAnswerTemplate

Overrides rendered confirmation line.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithAnswerTemplate(`{{ green "selected:" }} {{ label .Answer }}`),
)
```

## WithFieldTemplate

Deprecated helper for field-based templates.

```go
city, err := tui.Dropdown("City", data,
	tui.WithFieldTemplate("Name"),
)
```

Prefer `WithTemplate` for new code.

## WithContext

Sets dropdown context.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithContext(ctx),
)
```

## WithTimeout

Adds timeout cancellation.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithTimeout(30*time.Second),
)
```

## WithInput

Overrides input source.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithInput(in),
)
```

## WithOutput

Overrides output target.

```go
city, err := tui.Dropdown("City", cities,
	tui.WithOutput(out),
)
```

## WithOptions

Bundles multiple options for reuse.

```go
opts := tui.WithOptions(
	tui.WithTimeout(30*time.Second),
	tui.WithHide(),
)

city, err := tui.Dropdown("City", cities, opts)
```

## Default templates

Exported defaults:

- `DefaultLabelTemplate`
- `DefaultDropdownActiveItemTemplate`
- `DefaultDropdownInactiveItemTemplate`
- `DefaultMoreItemsTemplate`
- `DefaultDropdownAnswerTemplate`

## Label resolution for struct items

`Dropdown` resolves labels in this order:

1. Explicit label tags (`header:"label"`, `tui:"label"`, or `label:"true"`)
2. Heuristic field names (`Label`, `Name`, `Title`, `Description`, `DisplayName`, `FullName`, `Summary`, `Text`, `Subject`)
3. `fmt.Stringer`
4. `fmt.Sprint`
