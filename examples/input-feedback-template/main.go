package main

import (
	"context"
	"fmt"

	"github.com/nfx/go-tui"
)

// main demonstrates custom feedback formatting for a validated input.
func main() {
	ctx := context.Background()
	// the feedback slot sees only .Message, so a plain one works without colors
	theme := tui.DefaultTheme()
	theme.Templates.Input.Feedback = `[error] {{ .Message }}`
	if err := tui.SetDefaultTheme(theme); err != nil {
		panic(err)
	}
	port, err := tui.Input(ctx, "Port:",
		tui.WithDefault("80"),
		tui.WithRegex(`[0-9]{2,5}`, "Enter 2-5 digits"),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("Port", port)
}
