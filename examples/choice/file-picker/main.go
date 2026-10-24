package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nfx/go-tui"
)

// repoRoot walks up from the working directory to the nearest .git entry,
// falling back to the working directory itself.
func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		if dir == filepath.Dir(dir) {
			return wd, nil
		}
	}
}

func main() {
	ctx := context.Background()
	root, err := repoRoot()
	if err != nil {
		panic(err)
	}
	path, err := tui.FilePicker(ctx, "Pick a Go or Markdown file",
		tui.WithStartDir(root),           // where to start browsing
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
