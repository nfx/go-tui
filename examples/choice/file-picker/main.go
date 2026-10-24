package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	path, err := tui.FilePicker(ctx, "Pick a Go or Markdown file",
		tui.WithStartDir(home),           // where to start browsing
		tui.WithExtensions(".go", ".md"), // only show matching files (directories stay)
		tui.WithShowHidden(),             // include dotfiles
		// tui.WithIgnoreUp(),              // forbid navigating above the start dir
		// tui.WithIgnoreDirs(),            // hide directories entirely
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("selected:", path)
}
