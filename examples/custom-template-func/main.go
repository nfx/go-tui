package main

import (
	"os"
	"strings"

	"github.com/nfx/go-tui"
)

type Pet struct {
	Name string
	Type string
}

func main() {
	// WithFn registers a function usable in every template (tables, dropdowns).
	tui.WithFn("shout", func(s string) string { return strings.ToUpper(s) + "!" })

	pets := []Pet{{"Fluffy", "Cat"}, {"Buddy", "Dog"}}
	err := tui.Table(os.Stdout, `{{ shout .Name | red }}	{{ .Type }}`, pets)
	if err != nil {
		panic(err)
	}
	pet, err := tui.Dropdown("Pet", pets,
		tui.WithActiveItemTemplate(`→ {{ shout .Name | bold }}`))
	if err != nil {
		panic(err)
	}
	println("selected", pet.Name)
}
