package main

import (
	"context"
	"fmt"
	"github.com/nfx/go-tui"
)

// main selects protocol keys while retaining access to their mapped ports.
func main() {
	ports := map[string]int{"http": 80, "https": 443, "ssh": 22}
	names, err := tui.MultichoiceKVK(context.Background(), "Protocols", ports, tui.WithDefault("https"))
	if err != nil {
		panic(err)
	}
	for _, name := range names {
		fmt.Println(name, ports[name])
	}
}
