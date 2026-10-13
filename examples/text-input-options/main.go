package main

import (
	"context"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	// WithDefault pre-fills the input, so that Enter accepts the suggestion.
	name, err := tui.Input(ctx, "Your name:", tui.WithDefault("Alice"))
	if err != nil {
		panic(err)
	}
	// Editing moves over clusters; long defaults scroll instead of wrapping.
	city, err := tui.Input(ctx, "City:", tui.WithDefault("東京 \U0001F1EF\U0001F1F5 cafe\u0301 \U0001F468\u200D\U0001F469\u200D\U0001F467"))
	if err != nil {
		panic(err)
	}
	// WithNonEmpty keeps asking until something is typed.
	email, err := tui.Input(ctx, "Your email:", tui.WithNonEmpty())
	if err != nil {
		panic(err)
	}
	// options also work for passwords
	secret, err := tui.Password(ctx, "Secret:", tui.WithNonEmpty())
	if err != nil {
		panic(err)
	}
	println("name:", name, "city:", city, "email:", email, "secret length:", len(secret))
}
