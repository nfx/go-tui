package main

import "github.com/nfx/go-tui"

func main() {
	// string arguments are URL-escaped before being substituted into the template.
	err := tui.Browserf("https://github.com/search?q=%s", "go tui & spinner")
	if err != nil {
		panic(err)
	}
	// a pre-encoded URL must be passed without arguments
	if err = tui.Browserf("https://example.com/?q=a%20b"); err != nil {
		panic(err)
	}
}
