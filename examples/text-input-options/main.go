package main

import "github.com/nfx/go-tui"

func main() {
	// WithDefault pre-fills the input, so that Enter accepts the suggestion.
	name, err := tui.Input("Your name:", tui.WithDefault("Alice"))
	if err != nil {
		panic(err)
	}
	// WithNonEmpty keeps asking until something is typed.
	email, err := tui.Input("Your email:", tui.WithNonEmpty())
	if err != nil {
		panic(err)
	}
	// options also work for passwords
	secret, err := tui.Password("Secret:", tui.WithNonEmpty())
	if err != nil {
		panic(err)
	}
	println("name:", name, "email:", email, "secret length:", len(secret))
}
