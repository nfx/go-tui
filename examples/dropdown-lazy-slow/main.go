package main

import (
	"fmt"
	"time"

	"github.com/nfx/go-tui"
)

func main() {
	emu := func(yield func(string, error) bool) {
		for i := 1; i <= 30; i++ {
			time.Sleep(150 * time.Millisecond)
			if !yield(fmt.Sprintf("item %02d", i), nil) {
				return
			}
		}
	}
	_, err := tui.DropdownLazy("Select (slow)", emu)
	if err != nil {
		panic(err)
	}
}
