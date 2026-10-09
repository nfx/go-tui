package main

import (
	"fmt"

	"github.com/nfx/go-tui"
)

func main() {
	items := []any{"red", "green", "blue", "cyan", "magenta", "yellow", "black", "white"}

	// DropdownIndex returns a position of the selected item instead of the item.
	idx, err := tui.DropdownIndex("Pick a color", items,
		// WithOptions bundles several options into one
		tui.WithOptions(
			tui.WithLabelTemplate(`{{ "🎨" }} {{ . | bold | underline }}`),
			tui.WithMoreItemsTemplate(` {{ dim "… and " .More " other colors" | italic }}`),
			tui.WithAnswerTemplate(`{{ green "✔ " .Label " is " }}{{ .Answer | bold }}`),
		),
		// WithDefault ranks items by similarity to the hint, and returns
		// immediately if exactly one item starts with it.
		tui.WithDefault("gre"),
		// WithOneReturn returns immediately once filtering leaves a single item.
		tui.WithOneReturn(),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("index:", idx, "item:", items[idx])
}
