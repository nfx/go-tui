package main

import "github.com/nfx/go-tui"

func main() {
	if !tui.Confirm("Delete all the things?") {
		println("Aborted")
		return
	}
	// Confirmf formats the action like fmt.Sprintf
	if tui.Confirmf("Really delete %d things?", 42) {
		println("Deleted")
	}
}
