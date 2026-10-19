package main

import (
	"context"
	"fmt"

	"github.com/nfx/go-tui"
)

// main demonstrates custom feedback formatting for a validated input.
func main() {
	ctx := context.Background()
	// the template sees only .Message, so a plain one works without colors
	port, err := tui.Input(ctx, "Port:",
		tui.WithDefault("80"),
		tui.WithRegex(`[0-9]{2,5}`, "Enter 2-5 digits"),
		tui.WithFeedbackTemplate(`[error] {{ .Message }}`),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("Port", port)
}
