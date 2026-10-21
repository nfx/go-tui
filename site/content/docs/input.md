---
title: "Input and Password"
weight: 40
---

APIs:

- `Input(ctx, label, opts...)`
- `Password(ctx, label, opts...)`

## Basic example

```go
value, err := tui.Input(ctx, "Project name")
password, err := tui.Password(ctx, "API token")
```

`Password` masks entered text and does not print an answer line.

Validate answers with [WithNonEmpty](#withnonempty), [WithRegex](#withregex),
or [WithValidation](#withvalidation), and customize rejection feedback with
[WithFeedbackTemplate](#withfeedbacktemplate).

## WithDefault

Sets initial text and cursor position. The text is `fmt.Sprint(d)`, so strings
keep their content exactly; `""`, `0` and `nil` are defaults too (`nil` is
`<nil>`).

```go
value, err := tui.Input(ctx, "Project name",
	tui.WithDefault("demo-service"),
)
```

### Without a terminal

When the output is not a terminal (a pipe, a redirected file, a `bytes.Buffer`),
wrapping the input in a `strings.Reader` does not make it one. `Input` and
`Password` then read nothing, print nothing and return the default after
running the validation rules once with the call context. A rejection is an
error wrapping `ErrInvalidOption` and the validator's own error, so
`errors.Is` finds both; it never loops. `Password` keeps the secret out of its
errors. Without a default the error wraps `ErrNoTTY`.

```go
name, err := tui.Input(ctx, "Project name",
	tui.WithDefault("demo-service"), tui.WithNonEmpty())
```

## WithNonEmpty

Rejects empty and whitespace-only input with `a value is required`. Accepted
text is returned unchanged, including leading or trailing whitespace.

```go
value, err := tui.Input(ctx, "Project name", tui.WithNonEmpty())
```

## WithRegex

Requires the entire answer to match a Go regular expression. You do not need
to add anchors. The supplied message is shown when the match fails.

```go
username, err := tui.Input(ctx, "Username",
	tui.WithNonEmpty(),
	tui.WithRegex(`[a-z][a-z0-9]{2,15}`,
		"Use 3-16 lowercase letters or digits, starting with a letter"),
)
```

A pattern that matches an empty string permits an empty answer; combine it
with [WithNonEmpty](#withnonempty) to require a value. An invalid pattern or a blank message
returns an error wrapping `ErrInvalidOption` before interaction starts.

## WithValidation

Use `WithValidation(func(context.Context, string) error)` for custom checks.
Return `nil` to accept the value or an error containing feedback to reject it.
Combine it with [WithNonEmpty](#withnonempty) and [WithRegex](#withregex)
to run basic checks before custom validation.

```go
username, err := tui.Input(ctx, "Username",
	tui.WithNonEmpty(),
	tui.WithRegex(`[a-z][a-z0-9]{2,15}`, "Use 3-16 lowercase letters or digits"),
	tui.WithValidation(func(ctx context.Context, value string) error {
		if value == "admin" || value == "root" {
			return errors.New("This name is reserved")
		}
		return nil
	}),
)
```

This example uses the standard `errors` package. A validator receives the prompt
context and runs synchronously: key handling waits until it returns. Pass that
context to network requests or other blocking work so cancellation can finish
promptly. Validation errors keep the prompt open; prompt cancellation returns
the context error. A nil validator returns `ErrInvalidOption`.

Password validators receive the unmasked value. Keep their error messages
independent of the secret: feedback is displayed even though the password is
masked and its answer line is hidden.

## WithFeedbackTemplate

Customizes the single feedback row for either `Input` or `Password` when
[WithNonEmpty](#withnonempty), [WithRegex](#withregex), or
[WithValidation](#withvalidation) rejects an answer.

```go
value, err := tui.Input(ctx, "Project name",
	tui.WithNonEmpty(),
	tui.WithFeedbackTemplate(`{{ yellow "Check: " }}{{ .Message }}`),
)
```

The template receives only `.Message`, with no typed value. The message has
line breaks and tabs replaced with spaces and terminal control characters
removed. The rendered feedback is limited to its first line and clipped to
the available terminal width. Keep custom templates to one row.

Without `WithFeedbackTemplate`, or with an empty one, feedback uses the
`Feedback` slot of the theme, `✖ message` by default; see [Themes](themes.md).
Templates support the same color and style helpers listed in
[Template Functions](template-functions.md). A password always shows
`Invalid value` instead of a custom validator message, which could repeat
the secret; messages of `WithNonEmpty` and `WithRegex` still show.
Showing rejection feedback requires two terminal rows; a shorter terminal
returns `ErrNoSpace`.

## WithInput

Overrides input source.

```go
value, err := tui.Input(ctx, "Project name",
	tui.WithInput(in),
)
```

## WithOutput

Overrides output destination.

```go
value, err := tui.Input(ctx, "Project name",
	tui.WithOutput(out),
)
```

## WithOptions

Bundles common options.

```go
common := tui.WithOptions(
	tui.WithOutput(os.Stderr),
)

value, err := tui.Input(ctx, "Project name", common)
```

## Notes

- Arrow-left/right and backspace editing are supported.
- Paste buffers are handled as one input event.
- Timeout/cancel returns context error.

`Input` and `Password` validate when you press Enter. Rules run in option order
and stop at the first error. A rejected answer stays editable, with the error
shown below it using [WithFeedbackTemplate](#withfeedbacktemplate). Editing the
text clears the feedback; moving the cursor keeps it visible. Validation runs
again on the next Enter.

Prompts can share the default terminal output with running progress bars and
spinners. Validation feedback stays below the input while progress updates
continue, including while a validator runs. Closing a progress bar or spinner
does not change the active prompt's terminal input mode. Route accompanying
logs through [`tui.Stderr()`](options-and-helpers.md#stderr) to keep them
coordinated with widget rendering.
