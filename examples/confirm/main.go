package main

import (
	"context"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	if !tui.Confirm(ctx, "Delete all the things?") {
		println("Aborted")
		return
	}
	// Confirmf formats the action like fmt.Sprintf
	if tui.Confirmf(ctx, "Really delete %d things?", 42) {
		println("Deleted")
	}
}
