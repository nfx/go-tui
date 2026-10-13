package main

import (
	"context"
	"fmt"
	"github.com/nfx/go-tui"
)

// main demonstrates distinct key-based choices returning equal port values.
func main() {
	// Equal payloads remain distinct choices; the key is displayed and searched.
	ports, err := tui.MultichoiceKVV(context.Background(), "Protocols", map[string]int{"http": 80, "https": 443, "https-alt": 443}, tui.WithDefault("https-alt"))
	if err != nil {
		panic(err)
	}
	fmt.Println(ports)
}
