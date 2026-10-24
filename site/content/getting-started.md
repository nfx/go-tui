---
title: "Getting Started"
weight: 10
---

Add prompts and progress to existing command handlers while keeping normal Go
control flow. See [Alternatives](alternatives.md) for its strengths and scope.

Install:

```shell
go get github.com/nfx/go-tui@latest
```

Minimal [dropdown](dropdowns.md#dropdown):

```go
package main

import (
	"context"
	"log"

	tui "github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	choice, err := tui.Dropdown(ctx, "Select environment", []string{"dev", "staging", "prod"})
	if err != nil {
		log.Fatal(err)
	}
	log.Println("selected:", choice)
}
```

Interactive widgets take a `context.Context` as the first argument.
Start with [Shared Options and Helpers](options.md), then see each widget page.

## Run the same command in automation

Supply an explicit default when a prompt should also work without a terminal:

```go
environment, err := tui.Dropdown(ctx, "Select environment",
	[]string{"dev", "staging", "prod"}, tui.WithDefault("dev"))
```

When output is redirected or the command runs in [CI](https://en.wikipedia.org/wiki/Continuous_integration), the prompt validates the
default and returns it without reading input or printing a prompt. Without a
default, this [dropdown](dropdowns.md#dropdown) returns an error wrapping [`tui.ErrNoTTY`](options.md#common-errors); an invalid or
ambiguous default wraps [`tui.ErrInvalidOption`](options.md#common-errors). Interactive default behavior
depends on the widget: a [dropdown](dropdowns.md#dropdown) can return a unique prefix match immediately.
See [Dropdowns](dropdowns.md) and [non-TTY defaults](options.md#defaults-without-a-terminal).

Use [`tui.Stderr()`](options.md#stderr) as your logger's writer when logs should coexist with widgets
on the default terminal. Direct writes to `os.Stderr` bypass that coordination.
