---
title: "Multichoice"
weight: 50
---

A multichoice prompt returns checked items. Use Up/Down to focus, Space to toggle,
and Enter to confirm. Enter does not check the focused item. Every invocation
requires confirmation, including a single item or a unique default.

## Multichoice

`Multichoice(ctx, label, []T, opts...)` returns `[]T` in original input order.
The eager input is shallowly copied. Checks belong to source entries,
independent of filtering, ranking, focus, or click order. Duplicate labels
remain independently selectable; values need not be comparable. Do not mutate
the input or referenced item internals concurrently.
No checks is a valid answer. Success always returns a fresh, non-nil
slice, including an empty slice. Every error returns a nil slice. Empty input
returns [`ErrNoItems`](options.md#common-errors).

{{< recorded name="choice/multichoice" title="Toggling services in a multichoice prompt" >}}

## MultichoiceKVK

`MultichoiceKVK(ctx, label, map[K]V, opts...)` shows the keys of a map and
returns the selected `[]K`.
The map is snapshotted once and sorted by `fmt.Sprint(key)`. Equal formatted
keys have unspecified relative order; pass an ordered slice to
[`Multichoice`](#multichoice) when a total order matters. When both keys and
values are needed from one interaction, call `MultichoiceKVK` and look up the
values in the unchanged map. Empty input returns
[`ErrNoItems`](options.md#common-errors).

{{< recorded name="choice/multichoice-kvk" title="Selecting map keys in a multichoice prompt" >}}

## MultichoiceKVV

`MultichoiceKVV(ctx, label, map[K]V, opts...)` also shows the keys, but returns
the selected `[]V`.
Ordering and snapshot rules match [`MultichoiceKVK`](#multichoicekvk). Duplicate
map values remain independently selectable, because each row is a key.

{{< recorded name="choice/multichoice-kvv" title="Selecting map values by their keys" >}}

## MultichoiceLazy

`MultichoiceLazy(ctx, label, iter.Seq2[T, error], opts...)` returns `[]T` in
producer arrival order after the finite sequence finishes. A nil sequence
returns [`ErrInvalidOption`](options.md#common-errors); an empty completed
sequence returns [`ErrEmptyLazyResult`](options.md#common-errors).
Choices appear progressively. Enter is disabled until successful exhaustion;
Enter during loading is not queued. Producer errors, including late errors,
abort with a nil result.

{{< recorded name="choice/multichoice-lazy" title="Choices arriving progressively in a lazy multichoice" >}}

Default resolution stays pending until all choices are known. A unique default
is added only if the user has not explicitly toggled it. An already checked
default stays checked. An explicitly unchecked default stays unchecked.
Focus follows the best-ranked arrival until the user navigates or filters;
after that, arrival and default resolution preserve the user's focus.

### Cancellation

Cancel/deadline errors remain discoverable through `errors.Is` with
`context.Canceled`/`context.DeadlineExceeded`. Ctrl-C, Ctrl-D, and input [EOF](https://en.wikipedia.org/wiki/End-of-file) use
`io.EOF`. Aborts print no answer. I/O, template, and producer errors preserve their
underlying errors. Resources are restored and the managed viewport released on
every exit, even while the parent context remains alive.

Publication is bounded and returns `yield(false)` on cancellation. An iterator
blocked in arbitrary caller code cannot be forcibly interrupted; blocking
sources must observe a context supplied by their caller. A blocked caller-owned
read is retained for its successor, rather than drained or replayed after use.
Use one active input prompt per shared reader. Independent terminals remain
independent. Default output participates in the terminal arbiter alongside logs
and progress; this widget does not provide simultaneous input prompt admission.

## Typed defaults

[`WithDefault`](options.md#defaults-without-a-terminal) also takes typed values, which are not labels. `Multichoice[T]`
and `MultichoiceLazy[T]` take a `T` or a `[]T`, `MultichoiceKVK` a key or `[]K`,
and `MultichoiceKVV` a value or `[]V` even though rows show keys. When `K` and
`V` are the same type, the function decides. Matching, uniqueness and rejected
values ([`ErrInvalidOption`](options.md#common-errors)) follow [dropdowns](dropdowns.md#withdefault). A
slice is resolved member by member and the result keeps source order, not
default order; a missing, repeated or ambiguous member fails. An empty `[]T` is
an empty selection and returns a non-nil empty slice; no default never selects
anything. When `T` is itself a slice or interface, a value that matches one item
wins, an ambiguous one is an error, and otherwise a `[]T` is a selection.
Members are values, never prefix hints.

```go
ports := map[string]int{"http": 80, "https": 443, "alt": 8443}
values, err := tui.MultichoiceKVV(ctx, "Ports", ports,
    tui.WithDefault([]int{443, 80}))
// []int{80, 443}
```

Without a terminal the default is the answer: nothing is read or printed, no
default is [`ErrNoTTY`](options.md#common-errors), and a sole checkbox is never selected for you.
[`WithOneReturn`](dropdowns.md#withonereturn) stays unsupported. `MultichoiceLazy` does not start its
producer without a default and checks the default after exhaustion.

## Defaults

[`WithDefault(hint)`](options.md#defaults-without-a-terminal) uses the existing case-insensitive word-prefix matcher and
similarity ranking. Exactly one prefix match starts checked and focused. Zero
matches, multiple matches, or duplicate labels start unchecked and focus the
highest-ranked choice. An exact label that also prefixes a longer label is
ambiguous. Ties keep source order. The hint never fills the typed filter and
never reapplies after the user unchecks it. Repeated hints replace earlier ones;
an empty hint disables default matching and ranking.

## Filtering and paste

Printable text filters choices by word prefix. When no choices match, the prompt
shows “No matches” and Space does nothing. Enter can confirm previously checked,
hidden items. Backspace removes one displayed text cluster; Escape clears a
nonempty query. Other unsupported control keys are ignored.

Space toggles even while filtering. Bracketed paste is explicitly delimited
filter text: spaces stay spaces, [CR](https://en.wikipedia.org/wiki/Carriage_return)/LF/tab become spaces, and other controls are
discarded. Payload characters never toggle or confirm. Ordinary buffered input
is decoded as keys regardless of read boundaries. Bytes after Enter stay with
the reader for the next prompt. An incomplete paste is discarded on EOF or
cancellation. Bracketed-paste terminal mode is enabled only during this prompt.

## Templates

Multichoice accepts the same [`WithTemplate`](dropdowns.md#withtemplate) as dropdown. Its label, rows, count
and answer line come from the `Multichoice` theme slots.
[`WithOneReturn`](dropdowns.md#withonereturn) is unsupported and returns [`ErrWrongWidget`](options.md#common-errors).

```go
type Service struct { Name, Region string }
selected, err := tui.Multichoice(ctx, "Services", services,
    tui.WithTemplate(".Name", ".Region"),
)
```

Item-content templates receive the raw item. The inactive main content supplies
search and default ranking text; active details do not affect matching. Focus
and checkbox decoration remain outside those templates. The theme slot
`Multichoice.Row` adds them, with `.Value`, `.Selected`, `.Active`,
and the original `.Index`; see [Themes](themes.md). Rows and multiline details are clipped at whole [ANSI](https://en.wikipedia.org/wiki/ANSI_escape_code)/Unicode
text boundaries and indicate omitted content with an ellipsis.

Both map [APIs](https://en.wikipedia.org/wiki/API) give content templates an entry with `.Key` and `.Value`. Both
display, search, and answer with keys by default. [`WithTemplate(".Value.Name")`](dropdowns.md#withtemplate)
presents values instead. Both answer collections contain entries with `.Key`
and `.Value`, regardless of the returned projection.

The answer slot has `.Label`, `.Values` (the projected content of the selected
items in source order), and `.Count`. Generated answers are collection-aware,
and an empty default answer explicitly says “none”. [`WithHideAnswer`](dropdowns.md#withhideanswer) suppresses the
answer. The count of matching choices below the window leads the hint row,
as in `3 more · ↑↓ move · space toggle · type filter`.
