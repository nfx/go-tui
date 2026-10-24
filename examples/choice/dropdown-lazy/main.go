package main

import (
	"context"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	var err error
	emu := func(yield func(string, error) bool) {
		for _, v := range []string{
			"1", "2", "3", "4", "5", "6", "7", "8", "9", "10",
			"11", "12", "13", "14", "15", "16", "17", "18", "19", "20",
			"21", "22", "23", "24", "25", "26", "27", "28", "29", "30"} {
			if !yield(v, nil) {
				return
			}
		}
	}
	_, err = tui.DropdownLazy(ctx, "Select", emu)
	if err != nil {
		panic(err)
	}
}
