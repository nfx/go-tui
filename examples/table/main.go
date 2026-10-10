package main

import (
	"os"

	"github.com/nfx/go-tui"
)

type Pet struct {
	Name string
	Age  int
	Type string
}

func main() {
	pets := []Pet{
		{"Fluffy", 3, "Cat"},
		{"Buddy", 5, "Dog"},
		{"Goldie", 1, "Fish"},
		{"Tweety", 2, "Bird"},
		// Table widths use columns, not bytes or runes.
		{"ポチ", 4, "犬"},
		{"Cafe\u0301", 6, "\U0001F431"},
		{"\U0001F468\u200D\U0001F469\u200D\U0001F467", 1, "\U0001F1FA\U0001F1E6"},
		// Raw escape sequences inside cells take no columns and are never cut.
		{"\x1b[7mInverted\x1b[0m", 7, "very long type that is truncated at the max width \x1b[31mred tail\x1b[0m"},
	}
	// Table renders a slice using an explicit row template, with columns split by tabs.
	// WithMaxWidth truncates the output to the given amount of characters.
	err := tui.Table(os.Stdout,
		`{{ green .Name | bold }}	{{ .Age }}	{{ yellow .Type }}`,
		pets, tui.WithMaxWidth(40))
	if err != nil {
		panic(err)
	}
}
