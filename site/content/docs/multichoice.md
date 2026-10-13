---
title: "Multichoice"
weight: 7
---

A multichoice prompt returns checked items. Use Up/Down to focus, Space to toggle,
and Enter to confirm. Enter does not check the focused item. Every invocation
requires confirmation, including a single item or a unique default.

```go
regions, err := tui.Multichoice(ctx, "Deploy to regions",
    []string{"Amsterdam", "Berlin", "Paris"},
    tui.WithDefault("am"),
)
```

## APIs and result order

- `Multichoice(ctx, label, []T, opts...)` returns `[]T` in original input order.
- `MultichoiceKVK(ctx, label, map[K]V, opts...)` returns `[]K`.
- `MultichoiceKVV(ctx, label, map[K]V, opts...)` returns `[]V`.
- `MultichoiceLazy(ctx, label, iter.Seq2[T, error], opts...)` returns `[]T` in
  producer arrival order after the finite sequence finishes.

The eager input is shallowly copied. Maps are snapshotted once and sorted by
`fmt.Sprint(key)`. Equal formatted keys have unspecified relative order; pass an
ordered slice when a total order matters. Checks belong to source entries,
independent of filtering, ranking, focus, or click order. Duplicate labels and
duplicate map values remain independently selectable; values need not be
comparable. Do not mutate input maps or referenced item internals concurrently.

No checks is a valid answer. Success always returns a fresh, non-nil
slice, including an empty slice. Every error returns a nil slice. Empty eager
input returns `ErrNoItems`; an empty completed sequence returns
`ErrEmptyLazyResult`. A nil sequence returns `ErrInvalidOption`.

Each map API opens its own prompt. When both keys and values are needed from one
interaction, call `MultichoiceKVK` and look up the values in the unchanged map.

```go
ports := map[string]int{"http": 80, "https": 443}
names, err := tui.MultichoiceKVK(ctx, "Protocols", ports,
    tui.WithDefault("https"))
// Enter immediately returns []string{"https"}.

selectedPorts, err := tui.MultichoiceKVV(ctx, "Protocols", ports,
    tui.WithDefault("https"))
// A separate interaction; Enter immediately returns []int{443}.
```

## Defaults

`WithDefault(hint)` uses the existing case-insensitive word-prefix matcher and
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
filter text: spaces stay spaces, CR/LF/tab become spaces, and other controls are
discarded. Payload characters never toggle or confirm. Ordinary buffered input
is decoded as keys regardless of read boundaries. Bytes after Enter stay with
the reader for the next prompt. An incomplete paste is discarded on EOF or
cancellation. Bracketed-paste terminal mode is enabled only during this prompt.

## Templates

Multichoice accepts the same `WithTemplate`, active/inactive item, label, answer,
and more-items options as dropdown. Deprecated `WithFieldTemplate` also works.
`WithOneReturn` is unsupported; direct use returns `ErrWrongWidget`. `WithOptions`
keeps its usual skip semantics for unsupported members.

```go
type Service struct { Name, Region string }
selected, err := tui.Multichoice(ctx, "Services", services,
    tui.WithTemplate(".Name", ".Region"),
    tui.WithAnswerTemplate(
        `{{.Label}}:{{range .Answer}} {{.Name}}{{else}} none{{end}}`),
)
```

Item-content templates receive the raw item. The inactive main content supplies
search and default ranking text; active details do not affect matching. Focus
and checkbox decoration remain outside those templates. The default row uses
`DefaultMultichoiceItemTemplate` with `.Value`, `.Selected`, `.Active`, and the
original `.Index`. Rows and multiline details are clipped at whole ANSI/Unicode
text boundaries and indicate omitted content with an ellipsis.

Both map APIs give content templates an entry with `.Key` and `.Value`. Both
display, search, and answer with keys by default. `WithTemplate(".Value.Name")`
presents values instead. Both answer collections contain entries with `.Key`
and `.Value`, regardless of the returned projection.

Label dot is the label string. Answer data has `.Label`, `.Answer` (the selected
items in source order), and `.Count`. Generated answers are collection-aware,
and an empty default answer explicitly says “none”. `WithHide` suppresses the
answer. The more-items template receives `.More` (matching choices below the
window) and `.Total` (all matching choices).

## Lazy loading and cancellation

Choices appear progressively. Enter is disabled until successful exhaustion;
Enter during loading is not queued. Producer errors, including late errors,
abort with a nil result.

```go
seq := func(yield func(string, error) bool) {
    for _, name := range []string{"api", "worker", "scheduler"} {
        if !yield(name, nil) { return }
    }
}
selected, err := tui.MultichoiceLazy(ctx, "Components", seq,
    tui.WithDefault("api"))
```

Default resolution stays pending until all choices are known. A unique default
is added only if the user has not explicitly toggled it. An already checked
default stays checked. An explicitly unchecked default stays unchecked.
Focus follows the best-ranked arrival until the user navigates or filters;
after that, arrival and default resolution preserve the user's focus.

Cancel/deadline errors remain discoverable through `errors.Is` with
`context.Canceled`/`context.DeadlineExceeded`. Ctrl-C, Ctrl-D, and input EOF use
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

Runnable examples are in `examples/multichoice`, `examples/multichoice-kvk`,
`examples/multichoice-kvv`, and `examples/multichoice-lazy`.
