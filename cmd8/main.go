package main

import (
	"iter"
	"os"

	"github.com/nfx/go-tui"
)

type Pet struct {
	Name  string
	Age   int
	Type  string
	Owner Person
}

type Person struct {
	Name string
	Age  int
}

var dummyPets = []Pet{
	{"Fluffy", 3, "Cat", Person{"Alice", 30}},
	{"Buddy", 5, "Dog", Person{"Bob", 25}},
	{"Goldie", 1, "Fish", Person{"Charlie", 20}},
	{"Tweety", 2, "Bird", Person{"Diana", 35}},
	{"Nemo", 1, "Fish", Person{"Eve", 28}},
	{"Max", 4, "Dog", Person{"Frank", 40}},
	{"Whiskers", 2, "Cat", Person{"Grace", 22}},
	{"Fluffy", 3, "Cat", Person{"Alice", 30}},
	{"Buddy", 5, "Dog", Person{"Bob", 25}},
	{"Goldie", 1, "Fish", Person{"Charlie", 20}},
	{"Tweety", 2, "Bird", Person{"Diana", 35}},
	{"Nemo", 1, "Fish", Person{"Eve", 28}},
	{"Max", 4, "Dog", Person{"Frank", 40}},
	{"Whiskers", 2, "Cat", Person{"Grace", 22}},
	{"Fluffy", 3, "Cat", Person{"Alice", 30}},
	{"Buddy", 5, "Dog", Person{"Bob", 25}},
	{"Goldie", 1, "Fish", Person{"Charlie", 20}},
	{"Tweety", 2, "Bird", Person{"Diana", 35}},
	{"Nemo", 1, "Fish", Person{"Eve", 28}},
	{"Max", 4, "Dog", Person{"Frank", 40}},
	{"Whiskers", 2, "Cat", Person{"Grace", 22}},
	{"Fluffy", 3, "Cat", Person{"Alice", 30}},
	{"Buddy", 5, "Dog", Person{"Bob", 25}},
	{"Goldie", 1, "Fish", Person{"Charlie", 20}},
	{"Tweety", 2, "Bird", Person{"Diana", 35}},
	{"Nemo", 1, "Fish", Person{"Eve", 28}},
	{"Max", 4, "Dog", Person{"Frank", 40}},
	{"Whiskers", 2, "Cat", Person{"Grace", 22}}, {"Fluffy", 3, "Cat", Person{"Alice", 30}},
	{"Buddy", 5, "Dog", Person{"Bob", 25}},
	{"Goldie", 1, "Fish", Person{"Charlie", 20}},
	{"Tweety", 2, "Bird", Person{"Diana", 35}},
	{"Nemo", 1, "Fish", Person{"Eve", 28}},
	{"Max", 4, "Dog", Person{"Frank", 40}},
	{"Whiskers", 2, "Cat", Person{"Grace", 22}},
}

func iterate[T any](items []T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for _, v := range items {
			if !yield(v, nil) {
				return
			}
		}
	}
}

func main() {
	err := tui.Table(os.Stdout,
		"{{ green .Name | italic | underline }}\t{{ dim .Age }}\t{{ yellow .Type }}\t{{.Owner.Name}}\t{{.Owner.Age}}",
		iterate(dummyPets))
	if err != nil {
		panic(err)
	}
}
