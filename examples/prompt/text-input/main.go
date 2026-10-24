package main

import (
	"context"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	result, err := tui.Input(ctx, "Enter something:")
	if err != nil {
		panic(err)
	}
	println("You entered:", result)
}
