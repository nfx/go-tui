---
title: "Input and Password"
weight: 20
---

[`Input`](#input) and [`Password`](#password) validate when you press Enter. 
Arrow-left/right and backspace editing are supported. Paste buffers are handled 
as one input event. Timeout/cancel returns context error.
Validate answers with [WithNonEmpty](#withnonempty), [WithRegex](#withregex),
or [WithValidation](#withvalidation), and customize rejection feedback with
the [feedback slot](#feedback-row) of the theme. Rules run in option order
and stop at the first error. A rejected answer stays editable, with the error
shown below it using the [feedback row](#feedback-row). Editing the
text clears the feedback; moving the cursor keeps it visible. Validation runs
again on the next Enter.

## Input

`Input(ctx, label, opts...)` expects a text from the user.

{{< recorded name="prompt/text-input" title="A basic text input prompt" >}}

## Password

`Password(ctx, label, opts...)` masks entered text and does not print an answer line.

{{< recorded name="prompt/password" title="A masked password prompt" >}}

## WithDefault

Sets initial text and cursor position. The text is `fmt.Sprint(d)`, so strings
keep their content exactly; `""`, `0` and `nil` are defaults too (`nil` is
`<nil>`).

{{< recorded name="prompt/text-input-options" title="Default text, Unicode editing and required input" >}}

### Without a terminal

When the output is not a terminal (a pipe, a redirected file, a `bytes.Buffer`),
wrapping the input in a `strings.Reader` does not make it one. `Input` and
`Password` then read nothing, print nothing and return the default after
running the validation rules once with the call context. A rejection is an
error wrapping [`ErrInvalidOption`](options.md#common-errors) and the validator's own error, so
`errors.Is` finds both; it never loops. `Password` keeps the secret out of its
errors. Without a default the error wraps [`ErrNoTTY`](options.md#common-errors).

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
returns an error wrapping [`ErrInvalidOption`](options.md#common-errors) before interaction starts.

## WithValidation

Use [`WithValidation(func(context.Context, string) error)`](#withvalidation) for custom checks.
Return `nil` to accept the value or an error containing feedback to reject it.
Combine it with [WithNonEmpty](#withnonempty) and [WithRegex](#withregex)
to run basic checks before custom validation.

{{< recorded name="prompt/input-validation" title="Rejected usernames keep the prompt open" >}}

Password validators receive the unmasked value, but the user never sees their
error messages: a rejected password shows `Invalid value` instead, so a message
cannot repeat the secret. See the [feedback row](#feedback-row).
Messages of [`WithNonEmpty`](#withnonempty) and [`WithRegex`](#withregex) still show.

{{< recorded name="prompt/password-validation" title="A rejected password shows a generic message" >}}

## Feedback row

The single feedback row for either `Input` or `Password` is drawn by the
`Feedback` slot of the theme when [WithNonEmpty](#withnonempty),
[WithRegex](#withregex), or [WithValidation](#withvalidation) rejects an answer.

The slot receives only `.Message`, with no typed value. The message has
line breaks and tabs replaced with spaces and terminal control characters
removed. The rendered feedback is limited to its first line and clipped to
the available terminal width. Keep custom templates to one row.

{{< recorded name="prompt/input-feedback-template" title="A custom validation feedback row" >}}

By default feedback is `✖ message`; see [Themes](themes.md).
Templates support the same color and style helpers listed in
[Template functions](themes.md#template-functions). A password always shows
`Invalid value` instead of a custom validator message, which could repeat
the secret; messages of [`WithNonEmpty`](#withnonempty) and [`WithRegex`](#withregex) still show.
Showing rejection feedback requires two terminal rows; a shorter terminal
returns [`ErrNoSpace`](options.md#common-errors).

## Stderr

Prompts can share the default terminal output with running [progress bars](progressbar.md) and
[spinners](spinners.md). [Validation feedback](#feedback-row) stays below the input while progress updates
continue, including while a validator runs. Closing a progress bar or spinner
does not change the active prompt's terminal input mode. Route accompanying
[logs](alternatives.md#prompts-progress-and-logs-together) through [`tui.Stderr()`](options.md#stderr) to keep them coordinated with widget rendering.

{{< recorded name="prompt/input-validation-progress" title="A validated prompt next to a running progress bar" >}}