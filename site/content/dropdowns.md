---
title: "Dropdowns"
weight: 40
---

## Dropdown

Selects a value from a typed list.

Use this for the common case where you need the selected value.

{{< demo src="choice/dropdown" alt="Selecting a city from a dropdown" >}}

{{< example name="choice/dropdown" >}}

## DropdownIndex

Like [`Dropdown`](#dropdown), but returns the selected index in `[]any`.

Use this when you want to keep value lookup separate.

{{< demo src="choice/dropdown-index" alt="Selecting an item and printing its index" >}}

{{< example name="choice/dropdown-index" >}}

## DropdownKV

Selects from a map and returns both key and value.

The map is sorted by key string representation before rendering.

{{< demo src="choice/dropdown-kv" alt="Selecting a key and value from a map" >}}

{{< example name="choice/dropdown-kv" >}}

## DropdownLazy

Streams items progressively via `iter.Seq2`.

Use this for large or slow data sources.

{{< demo src="choice/dropdown-lazy" alt="Items appearing progressively in a lazy dropdown" >}}

{{< example name="choice/dropdown-lazy" >}}

A slow producer keeps the prompt usable while items arrive:

{{< demo src="choice/dropdown-lazy-slow" alt="A lazy dropdown fed by a slow producer" >}}

{{< example name="choice/dropdown-lazy-slow" >}}

## Confirm

Renders a yes/no dropdown and returns `true` for "yes".

{{< demo src="choice/confirm" alt="Confirm and Confirmf prompts" >}}

{{< example name="choice/confirm" >}}

## Confirmf

Formatted variant of [`Confirm`](#confirm).

```go
ok := tui.Confirmf(ctx, "Delete %d files?", 12)
if !ok {
	return nil
}
```

## WithDefault

Sets a match hint, not a pre-filled filter. The dropdown ranks items by similarity to the hint and returns early only when exactly one item matches it as a prefix; the typed filter stays empty. Also useful with [`WithOneReturn`](#withonereturn).

```go
city, err := tui.Dropdown(ctx, "City", cities,
	tui.WithDefault("am"),
)
```

A default can also be a typed value. It is kept as is, never turned into a
label: [`Dropdown[T]`](#dropdown), [`DropdownLazy[T]`](#dropdownlazy) and [`DropdownIndex`](#dropdownindex) take the item,
[`DropdownKV`](#dropdownkv) takes the **key** (a value of the map is not looked up). It must be
assignable to the item type, without conversions, and is compared with `==`, or
with `reflect.DeepEqual` for slices and maps; pointers compare by identity. It
must identify exactly one item: a value that is missing, repeated, a non-nil
function or `NaN` fails with [`ErrInvalidOption`](options.md#common-errors). An untyped `nil` matches a nil
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

When the output is not a terminal, every dropdown answers from [`WithDefault`](options.md#defaults-without-a-terminal)
without reading input or printing anything, and fails otherwise:

- no default: an error wrapping [`ErrNoTTY`](options.md#common-errors); [`DropdownLazy`](#dropdownlazy) does not start its
  producer;
- a missing, ambiguous or wrongly typed default: [`ErrInvalidOption`](options.md#common-errors);
- a string is first an exact item, so `"a"` selects `"a"` even when `"ab"` exists;
  otherwise it is a word-prefix hint that must match exactly one label;
- with [`WithOneReturn`](#withonereturn), a single item is returned without a default, ahead of any hint.

[`DropdownLazy`](#dropdownlazy) consumes the whole sequence first, so a later duplicate or a late
producer error is not hidden; an empty sequence is [`ErrEmptyLazyResult`](options.md#common-errors).

[`Confirm`](#confirm) keeps its `bool` result and cannot show errors: [`WithDefault(true)`](options.md#defaults-without-a-terminal) or
`"Yes"` returns true, [`WithDefault(false)`](#withdefault) or `"No"` false, and no default, an
invalid default or any error returns false. Call [`Dropdown`](#dropdown) with `"Yes"` and
`"No"` to see [`ErrNoTTY`](options.md#common-errors).

## WithOneReturn

Returns immediately if exactly one item is available.

```go
city, err := tui.Dropdown(ctx, "City", []string{"Amsterdam"},
	tui.WithOneReturn(),
)
```

## WithHideAnswer

Hides the final answer line after selection.

```go
city, err := tui.Dropdown(ctx, "City", cities,
	tui.WithHideAnswer(),
)
```

To hide answers by default, set `HideAnswer` on the [theme](themes.md) component, such as
[`Input`](input.md), [`Number`](number.md), [`Dropdown`](#dropdown), [`Multichoice`](multichoice.md), [`Confirm`](#confirm) or [`FilePicker`](filepicker.md#filepicker). An
option cannot show an answer that the theme hides, and passwords never keep one.

## WithTemplate

Sets main and active-details templates in one call.

Use this to choose the fields an item shows.

{{< demo src="choice/dropdown-customize" alt="A dropdown with a custom focus mark and template" >}}

{{< example name="choice/dropdown-customize" >}}

## Presentation

A dropdown draws through the theme. Set the slots once with
[SetDefaultTheme](themes.md); the same slots serve [`Confirm`](#confirm) and [`FilePicker`](filepicker.md#filepicker)
unless a family slot overrides them.

| Slot | Payload | Draws |
| --- | --- | --- |
| `Dropdown.Label` | `.Label` | the prompt label |
| `Dropdown.Active` | `.Value`, `.Details`, `.Index` | the focused row |
| `Dropdown.Inactive` | `.Value`, `.Index` | the other rows |
| `Dropdown.Hint` | a list with `.Left`, `.Right` | the key hints, led by the count of hidden items |
| `Dropdown.Answer` | `.Label`, `.Value` | the confirmation line |

```go
th := tui.DefaultTheme()
th.Dropdown.Label = `{{ accent "Select:" }} {{ bold .Label }}`
th.Dropdown.Active = `{{ success .Value }}`
th.Dropdown.Hint = `{{ range . }}{{ .Left }}={{ .Right }} {{ end }}`
th.Dropdown.Answer = `{{ success "selected:" }} {{ .Value }}`
if err := tui.SetDefaultTheme(th); err != nil {
	return err
}
city, err := tui.Dropdown(ctx, "City", cities)
```

`.Value` is the item as projected by [`WithTemplate`](#withtemplate), so a struct item needs a
projection to show its fields.

## Default appearance

A dropdown draws through the installed theme:
the [`Dropdown`](#dropdown) slots, which inherit from `Prompt` and
`Choice`. See [Themes](themes.md) to change it for every widget.

## Label resolution for struct items

[`Dropdown`](#dropdown) resolves labels in this order:

1. Explicit label tags (`header:"label"`, `tui:"label"`, or `label:"true"`)
2. Heuristic field names (`Label`, `Name`, `Title`, `Description`, `DisplayName`, `FullName`, `Summary`, `Text`, `Subject`)
3. `fmt.Stringer`
4. `fmt.Sprint`
