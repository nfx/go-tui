package main

import (
	"context"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	result, err := tui.Password(ctx, "Enter password:")
	if err != nil {
		panic(err)
	}
	// Masked input moves and deletes by rune, showing one mask per rune.
	println("You entered", len([]rune(result)), "runes")
}
