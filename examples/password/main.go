package main

import "github.com/nfx/go-tui"

func main() {
	result, err := tui.Password("Enter password:")
	if err != nil {
		panic(err)
	}
	// Masked input moves and deletes by rune, showing one mask per rune.
	println("You entered", len([]rune(result)), "runes")
}
