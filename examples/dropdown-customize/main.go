package main

import (
	"context"
	"fmt"
	"time"

	"github.com/nfx/go-tui"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// presentation lives in the theme: a custom focus mark and active row
	theme := tui.DefaultTheme()
	theme.Symbols.Focus = "→"
	theme.Dropdown.Active = `{{ bold .Value }}{{ with .Details }} {{ dim . }}{{ end }}`
	if err := tui.SetDefaultTheme(theme); err != nil {
		panic(err)
	}
	v, err := tui.Dropdown(ctx, "Samples", []Sample{
		{Name: "Alice", Age: 20},
		{Name: "Bob", Age: 30},
		{Name: "Василь", Age: 50},
		{Name: "Charlie", Age: 40},
	},
		// the projection picks the fields; details show on the active row only
		tui.WithTemplate(`.Name`, `"(age: " .Age ")"`),
		tui.WithHideAnswer(),
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Hello, %s!\n", v.Name)
}

type Sample struct {
	Name string
	Age  int
}
