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
