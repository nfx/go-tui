package main

import (
	"context"
	"fmt"
	"github.com/nfx/go-tui"
)

type Service struct{ Name, Region string }

// main demonstrates typed choices with active details and a confirmed default.
func main() {
	services := []Service{{"api", "eu"}, {"worker", "us"}, {"scheduler", "eu"}}
	selected, err := tui.Multichoice(context.Background(), "Services", services,
		tui.WithTemplate(".Name", ".Region"), tui.WithDefault("api"))
	if err != nil {
		panic(err)
	}
	fmt.Println(selected)
}
