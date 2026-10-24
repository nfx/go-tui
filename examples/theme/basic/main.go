package main

import (
	"context"
	"github.com/nfx/go-tui/theme"
	"os"
	"strings"

	"github.com/nfx/go-tui"
)

// main demonstrates a shared theme for prompts and facts.
func main() {
	// Configure the appearance once, before any UI work. Every prompt,
	// spinner, progress bar, table and JSON output started afterwards uses it.
	th := theme.New()
	th.Palette.Accent = "#00ADD8"
	th.Inline = len(os.Args) > 1 && os.Args[1] == "inline"
	th.Funcs = map[string]any{"shout": strings.ToUpper}
	th.Prompt.Label = `{{ accent (symbol "Prompt") }} {{ bold (shout .Label) }}`
	if err := tui.SetDefaultTheme(th); err != nil {
		panic(err)
	}

	ctx := context.Background()
	name, err := tui.Input(ctx, "Name", tui.WithDefault("gopher"))
	if err != nil {
		panic(err)
	}
	color, err := tui.Dropdown(ctx, "Color", []string{"blue", "green", "orange"}, tui.WithDefault("blue"))
	if err != nil {
		panic(err)
	}
	err = tui.Facts(os.Stdout, struct{ Name, Color string }{name, color})
	if err != nil {
		panic(err)
	}
}
