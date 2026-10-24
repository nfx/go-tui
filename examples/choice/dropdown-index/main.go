package main

import (
	"context"
	"fmt"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	items := []any{"red", "green", "blue", "cyan", "magenta", "yellow", "black", "white"}

	// the theme owns the label and the answer line
	theme := tui.DefaultTheme()
	theme.Dropdown.Label = `{{ "🎨" }} {{ .Label | bold | underline }}`
	theme.Dropdown.Answer = `{{ success "✔ " }}{{ .Label }} is {{ .Value | bold }}`
	if err := tui.SetDefaultTheme(theme); err != nil {
		panic(err)
	}

	// DropdownIndex returns a position of the selected item instead of the item.
	idx, err := tui.DropdownIndex(ctx, "Pick a color", items,
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
