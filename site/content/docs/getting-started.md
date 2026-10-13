---
title: "Getting Started"
weight: 10
---

Install:

```shell
go get github.com/nfx/go-tui
```

Minimal dropdown:

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

Most widgets accept shared options such as `WithInput` and `WithOutput`; interactive widgets take a `context.Context` as the first argument.
Start with [Shared Options and Helpers](options-and-helpers.md), then see each widget page.
