package main

import (
	"context"
	"fmt"

	"github.com/nfx/go-tui"
)

type Port uint16

// main demonstrates numeric entry and stepping.
func main() {
	ctx := context.Background()
	workers, err := tui.Number[int](ctx, "Workers",
		tui.WithDefault(4),
		tui.WithAtLeast(1),
		tui.WithAtMost(64))
	if err != nil {
		panic(err)
	}
	port, err := tui.Number[Port](ctx, "Port",
		tui.WithDefault(Port(8080)),
		tui.WithAtLeast(Port(1024)),
		tui.WithAtMost(Port(65535)))
	if err != nil {
		panic(err)
	}
	// move the caret with Left/Right, then press Up/Down to change that digit
	gain, err := tui.Number[float32](ctx, "Gain",
		tui.WithDefault("1.00"),
		tui.WithAtLeast(float32(0)),
		tui.WithAtMost(float32(2.5)))
	if err != nil {
		panic(err)
	}
	fmt.Println(workers, port, gain)
}
